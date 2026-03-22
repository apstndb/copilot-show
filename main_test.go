package main

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/apstndb/copilot-show/pkg/modeldocs"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestResolveVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		explicit string
		info     *debug.BuildInfo
		ok       bool
		want     string
	}{
		{
			name:     "explicit build flag wins",
			explicit: "v0.1.6",
			want:     "v0.1.6",
		},
		{
			name: "module version",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.1.6"},
			},
			ok:   true,
			want: "v0.1.6",
		},
		{
			name: "vcs revision",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef"},
				},
			},
			ok:   true,
			want: "0123456789ab",
		},
		{
			name: "dirty vcs revision",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			ok:   true,
			want: "0123456789ab-dirty",
		},
		{
			name: "devel fallback",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
			},
			ok:   true,
			want: "(devel)",
		},
		{
			name: "unknown without build info",
			ok:   false,
			want: "(unknown)",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := resolveVersion(tc.explicit, tc.info, tc.ok)
			if got != tc.want {
				t.Fatalf("resolveVersion(%q, %+v, %v) = %q, want %q", tc.explicit, tc.info, tc.ok, got, tc.want)
			}
		})
	}
}

func TestFormatModelDocsLiveMultipliers(t *testing.T) {
	t.Parallel()

	zero := 0.0
	one := 1.0

	tests := []struct {
		name    string
		matches []modeldocs.LiveMatch
		want    string
	}{
		{
			name: "no matches",
			want: "-",
		},
		{
			name: "no live billing multiplier",
			matches: []modeldocs.LiveMatch{
				{ID: "gpt-5.4", Name: "GPT-5.4"},
			},
			want: "-",
		},
		{
			name: "included multiplier",
			matches: []modeldocs.LiveMatch{
				{ID: "gpt-4.1", Name: "GPT-4.1", BillingMultiplier: &zero},
			},
			want: "Included (0)",
		},
		{
			name: "deduplicated multipliers",
			matches: []modeldocs.LiveMatch{
				{ID: "gpt-5.4", Name: "GPT-5.4", BillingMultiplier: &one},
				{ID: "gpt-5.4-alt", Name: "GPT-5.4 Alt", BillingMultiplier: &one},
			},
			want: "1",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := formatModelDocsLiveMultipliers(tc.matches)
			if got != tc.want {
				t.Fatalf("formatModelDocsLiveMultipliers(%#v) = %q, want %q", tc.matches, got, tc.want)
			}
		})
	}
}

func TestBuildCLIFocusedModelDocsSnapshot(t *testing.T) {
	t.Parallel()

	multiplier := 1.0
	zero := 0.0
	snapshot := modeldocs.Snapshot{
		CatalogVersion: "catalog",
		SourceNote:     "note",
		Sources: modeldocs.Sources{
			ReleaseStatus: "release",
		},
		LoadedFrom:   "embedded",
		LoadWarnings: []string{"warn"},
		Models: []modeldocs.JoinedModel{
			{
				Name:          "GPT-5.4",
				Provider:      "OpenAI",
				ReleaseStatus: "GA",
				Modes: modeldocs.ModeAvailability{
					Agent: true,
					Ask:   true,
					Edit:  true,
				},
				Clients: modeldocs.ClientAvailability{
					CLI:    true,
					VSCode: true,
				},
				Plans: modeldocs.PlanAvailability{
					Pro: true,
				},
				Multipliers: &modeldocs.RequestMultipliers{
					Paid: &multiplier,
					Free: &zero,
				},
				VisibleNow: true,
				LiveModels: []modeldocs.LiveMatch{
					{
						ID:                "gpt-5.4",
						Name:              "GPT-5.4",
						BillingMultiplier: &multiplier,
					},
				},
			},
			{
				Name:          "Claude Opus 4.1",
				Provider:      "Anthropic",
				ReleaseStatus: "Preview",
				Clients: modeldocs.ClientAvailability{
					CLI: true,
				},
				Plans: modeldocs.PlanAvailability{
					Enterprise: true,
				},
				VisibleNow: false,
			},
			{
				Name:          "GPT-4.5 Web",
				Provider:      "OpenAI",
				ReleaseStatus: "Preview",
				Clients: modeldocs.ClientAvailability{
					CLI: false,
				},
				Plans: modeldocs.PlanAvailability{
					Pro: true,
				},
				VisibleNow: true,
			},
		},
		RetiredModels: []modeldocs.RetiredModel{
			{Name: "GPT-5.1", RetirementDate: "2026-04-01"},
		},
		LiveModelsWithoutDocs: []modeldocs.LiveMatch{
			{ID: "custom", Name: "Custom"},
		},
	}

	got := buildCLIFocusedModelDocsSnapshot(snapshot)
	if got.CatalogVersion != snapshot.CatalogVersion || got.SourceNote != snapshot.SourceNote || got.LoadedFrom != snapshot.LoadedFrom {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() did not preserve top-level metadata: %#v", got)
	}
	if len(got.Models) != 2 {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() models len = %d, want 2", len(got.Models))
	}
	if got.Models[0].Provider != "OpenAI" || got.Models[0].ReleaseStatus != "GA" || !got.Models[0].VisibleNow {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() model = %#v", got.Models[0])
	}
	if got.Models[0].Multipliers == nil || got.Models[0].Multipliers.Paid == nil || *got.Models[0].Multipliers.Paid != 1 || got.Models[0].Multipliers.Free == nil || *got.Models[0].Multipliers.Free != 0 {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() multipliers = %#v", got.Models[0].Multipliers)
	}
	if len(got.Models[0].LiveModels) != 1 || got.Models[0].LiveModels[0].ID != "gpt-5.4" {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() liveModels = %#v", got.Models[0].LiveModels)
	}
	if got.Models[1].Name != "Claude Opus 4.1" || got.Models[1].VisibleNow {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() second model = %#v", got.Models[1])
	}
	if len(got.LiveModelsWithoutDocs) != 1 || got.LiveModelsWithoutDocs[0].ID != "custom" {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() liveModelsWithoutDocs = %#v", got.LiveModelsWithoutDocs)
	}
}

func TestBuildRuntimeModelsSnapshotPrefersDocsPaidMultiplier(t *testing.T) {
	t.Parallel()

	docsPaid := 0.33
	docsFree := 1.0
	liveDocsMultiplier := 9.0
	liveOnlyMultiplier := 2.0
	outputTokens := 4096.0
	vision := true
	reasoning := true

	got := buildRuntimeModelsSnapshot([]rpc.Model{
		{
			ID:   "claude-haiku-4.5",
			Name: "Claude Haiku 4.5",
			Billing: &rpc.Billing{
				Multiplier: liveDocsMultiplier,
			},
			Capabilities: rpc.Capabilities{
				Limits: rpc.Limits{
					MaxContextWindowTokens: 200000,
					MaxOutputTokens:        &outputTokens,
				},
				Supports: rpc.Supports{
					Vision:          &vision,
					ReasoningEffort: &reasoning,
				},
			},
			DefaultReasoningEffort:    strptr("medium"),
			SupportedReasoningEfforts: []string{"low", "medium", "high"},
			Policy: &rpc.Policy{
				State: "enabled",
			},
		},
		{
			ID:   "custom-runtime",
			Name: "Custom Runtime",
			Billing: &rpc.Billing{
				Multiplier: liveOnlyMultiplier,
			},
			Capabilities: rpc.Capabilities{
				Limits: rpc.Limits{
					MaxContextWindowTokens: 100000,
				},
			},
		},
	}, modeldocs.Snapshot{
		CatalogVersion: "catalog",
		LoadedFrom:     "embedded",
		LoadWarnings:   []string{"warn"},
		Sources: modeldocs.Sources{
			ModelMultipliers: "https://raw.githubusercontent.com/github/docs/main/data/tables/copilot/model-multipliers.yml",
		},
		Models: []modeldocs.JoinedModel{
			{
				Name: "Claude Haiku 4.5",
				Multipliers: &modeldocs.RequestMultipliers{
					Paid: &docsPaid,
					Free: &docsFree,
				},
			},
		},
	})

	if got.ModelCatalogVersion != "catalog" || got.ModelCatalogLoadedFrom != "embedded" || got.ModelCatalogSource == "" {
		t.Fatalf("buildRuntimeModelsSnapshot() metadata = %#v", got)
	}
	if got.MultiplierPlan != "paid" {
		t.Fatalf("buildRuntimeModelsSnapshot() multiplierPlan = %q, want paid", got.MultiplierPlan)
	}
	if len(got.Models) != 2 {
		t.Fatalf("buildRuntimeModelsSnapshot() models len = %d, want 2", len(got.Models))
	}
	if got.Models[0].MultiplierSource != "github/docs paid" || got.Models[0].SelectedMultiplier == nil || *got.Models[0].SelectedMultiplier != docsPaid {
		t.Fatalf("buildRuntimeModelsSnapshot() docs-backed model = %#v", got.Models[0])
	}
	if got.Models[0].DocsMultipliers == nil || got.Models[0].DocsMultipliers.Free == nil || *got.Models[0].DocsMultipliers.Free != docsFree {
		t.Fatalf("buildRuntimeModelsSnapshot() docs multipliers = %#v", got.Models[0].DocsMultipliers)
	}
	if got.Models[0].LiveMultiplier == nil || *got.Models[0].LiveMultiplier != liveDocsMultiplier {
		t.Fatalf("buildRuntimeModelsSnapshot() live multiplier = %#v", got.Models[0].LiveMultiplier)
	}
	if got.Models[0].MultiplierDisplay != "0.33" || !got.Models[0].SupportsVision || !got.Models[0].SupportsReasoning || got.Models[0].DefaultReasoningEffort != "medium" {
		t.Fatalf("buildRuntimeModelsSnapshot() first model view = %#v", got.Models[0])
	}
	if got.Models[1].MultiplierSource != "copilot-sdk live" || got.Models[1].SelectedMultiplier == nil || *got.Models[1].SelectedMultiplier != liveOnlyMultiplier {
		t.Fatalf("buildRuntimeModelsSnapshot() runtime-only model = %#v", got.Models[1])
	}
	if got.Models[1].DocsMultipliers != nil {
		t.Fatalf("buildRuntimeModelsSnapshot() runtime-only docs multipliers = %#v, want nil", got.Models[1].DocsMultipliers)
	}
}

func strptr(value string) *string {
	return &value
}

func TestParseSessionEventSupportsBestEffortTimestampFallbacks(t *testing.T) {
	t.Parallel()

	raw := map[string]any{
		"id":        "evt-nano",
		"type":      "session.start",
		"timestamp": "2026-03-22T14:32:57.823123456Z",
		"data": map[string]any{
			"sessionId": "session-1",
		},
	}

	got := parseSessionEvent(raw)
	if got == nil {
		t.Fatal("parseSessionEvent() returned nil for RFC3339Nano timestamp")
	}
	wantTime := time.Date(2026, 3, 22, 14, 32, 57, 823123456, time.UTC)
	if !got.Timestamp.Equal(wantTime) {
		t.Fatalf("parseSessionEvent() timestamp = %s, want %s", got.Timestamp.Format(time.RFC3339Nano), wantTime.Format(time.RFC3339Nano))
	}

	raw = map[string]any{
		"id":        "evt-ms",
		"type":      "session.future_notice",
		"timestamp": float64(1711111111000),
		"ephemeral": true,
		"data":      "new runtime-only payload",
	}

	got = parseSessionEvent(raw)
	if got == nil {
		t.Fatal("parseSessionEvent() returned nil for unix-millisecond timestamp")
	}
	wantTime = time.UnixMilli(1711111111000).UTC()
	if !got.Timestamp.Equal(wantTime) {
		t.Fatalf("parseSessionEvent() unix-ms timestamp = %s, want %s", got.Timestamp.Format(time.RFC3339Nano), wantTime.Format(time.RFC3339Nano))
	}
	if got.Data != nil {
		t.Fatalf("parseSessionEvent() data = %#v, want nil for non-object payload", got.Data)
	}
	if got.RawData != "new runtime-only payload" {
		t.Fatalf("parseSessionEvent() rawData = %#v, want scalar payload", got.RawData)
	}
	if got.Ephemeral == nil || !*got.Ephemeral {
		t.Fatalf("parseSessionEvent() ephemeral = %#v, want true", got.Ephemeral)
	}

	raw = map[string]any{
		"id":        "evt-ms-boundary",
		"type":      "session.future_notice",
		"timestamp": float64(999999999999),
	}

	got = parseSessionEvent(raw)
	if got == nil {
		t.Fatal("parseSessionEvent() returned nil for pre-1e12 unix-millisecond timestamp")
	}
	wantTime = time.UnixMilli(999999999999).UTC()
	if !got.Timestamp.Equal(wantTime) {
		t.Fatalf("parseSessionEvent() unix-ms boundary timestamp = %s, want %s", got.Timestamp.Format(time.RFC3339Nano), wantTime.Format(time.RFC3339Nano))
	}
}

func TestDescribeHistoryEventUsesGenericFallbackForUnknownTypes(t *testing.T) {
	t.Parallel()

	ev := &sessionEvent{
		ID:   "evt-1",
		Type: "mcp.oauth_required",
		Ephemeral: func() *bool {
			v := true
			return &v
		}(),
		Data: map[string]any{
			"message":       "Sign in to continue with the MCP server.",
			"requestId":     "req1234567890",
			"interactionId": "interaction-123456",
			"toolCallId":    "call-123456",
			"serverName":    "github",
			"status":        "pending",
		},
	}

	label, detail, extraLines := describeHistoryEvent(&historyRenderContext{}, ev)
	if label != "MCP Event" {
		t.Fatalf("describeHistoryEvent() label = %q, want %q", label, "MCP Event")
	}
	if detail != "mcp.oauth_required: Sign in to continue with the MCP server." {
		t.Fatalf("describeHistoryEvent() detail = %q", detail)
	}
	wantExtra := []string{
		"Ephemeral",
		"Request ID: " + shortID("req1234567890"),
		"Tool Call ID: " + shortID("call-123456"),
		"Interaction: " + shortID("interaction-123456"),
		"Status: pending",
	}
	if len(extraLines) != len(wantExtra) {
		t.Fatalf("describeHistoryEvent() extra lines len = %d, want %d (%#v)", len(extraLines), len(wantExtra), extraLines)
	}
	for i, want := range wantExtra {
		if extraLines[i] != want {
			t.Fatalf("describeHistoryEvent() extraLines[%d] = %q, want %q", i, extraLines[i], want)
		}
	}
}

func TestDescribeHistoryEventUsesScalarRawPayloadForFutureEvents(t *testing.T) {
	t.Parallel()

	ev := &sessionEvent{
		ID:      "evt-2",
		Type:    "session.future_notice",
		RawData: "A future Copilot CLI build emitted this row.",
	}

	label, detail, extraLines := describeHistoryEvent(&historyRenderContext{}, ev)
	if label != "Session Event" {
		t.Fatalf("describeHistoryEvent() label = %q, want %q", label, "Session Event")
	}
	if detail != "session.future_notice: A future Copilot CLI build emitted this row." {
		t.Fatalf("describeHistoryEvent() detail = %q", detail)
	}
	if len(extraLines) != 0 {
		t.Fatalf("describeHistoryEvent() extraLines = %#v, want none", extraLines)
	}
}

func TestIsKnownSDKSessionEventType(t *testing.T) {
	t.Parallel()

	if !isKnownSDKSessionEventType(copilot.SessionEventTypeUserMessage) {
		t.Fatal("isKnownSDKSessionEventType(user.message) = false, want true")
	}
	if isKnownSDKSessionEventType(copilot.SessionEventType("session.future_notice")) {
		t.Fatal("isKnownSDKSessionEventType(session.future_notice) = true, want false")
	}
}

func TestValidateSessionEventsAtPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	rows := []string{
		`{"id":"evt-1","type":"user.message","timestamp":"2026-03-22T14:32:57Z","data":{"content":"hello"}}`,
		`{"id":"evt-2","type":"session.future_notice","timestamp":"2026-03-22T14:32:58Z","data":{"message":"future but object"}}`,
		`{"id":"evt-3","type":"session.future_notice","timestamp":1711111111000,"data":"future scalar"}`,
		`{"id":"evt-4","type":"session.start","timestamp":1711111111000,"data":{"sessionId":"session-1"}}`,
	}
	if err := os.WriteFile(eventsPath, []byte(strings.Join(rows, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", eventsPath, err)
	}

	summary, err := validateSessionEventsAtPath("session-1", eventsPath, 10)
	if err != nil {
		t.Fatalf("validateSessionEventsAtPath() error = %v", err)
	}

	if summary.TotalRows != 4 {
		t.Fatalf("TotalRows = %d, want 4", summary.TotalRows)
	}
	if summary.SDKKnownTypeRows != 2 || summary.SDKUnknownTypeRows != 2 {
		t.Fatalf("known/unknown rows = %d/%d, want 2/2", summary.SDKKnownTypeRows, summary.SDKUnknownTypeRows)
	}
	if summary.SDKCompatibleRows != 2 || summary.SDKIncompatibleRows != 2 {
		t.Fatalf("sdk compatible/incompatible rows = %d/%d, want 2/2", summary.SDKCompatibleRows, summary.SDKIncompatibleRows)
	}
	if summary.LocalCompatibleRows != 4 || summary.LocalIncompatibleRows != 0 {
		t.Fatalf("local compatible/incompatible rows = %d/%d, want 4/0", summary.LocalCompatibleRows, summary.LocalIncompatibleRows)
	}
	if summary.LocalOnlyFallbackRows != 2 {
		t.Fatalf("LocalOnlyFallbackRows = %d, want 2", summary.LocalOnlyFallbackRows)
	}
	if summary.UnknownTypeSDKCompatibleRows != 1 {
		t.Fatalf("UnknownTypeSDKCompatibleRows = %d, want 1", summary.UnknownTypeSDKCompatibleRows)
	}
	if len(summary.IssueCounts) != 3 {
		t.Fatalf("IssueCounts len = %d, want 3 (%#v)", len(summary.IssueCounts), summary.IssueCounts)
	}
	if len(summary.Samples) != 3 {
		t.Fatalf("Samples len = %d, want 3 (%#v)", len(summary.Samples), summary.Samples)
	}

	wantIssues := map[string]int{
		"known-type-local-only-fallback":   1,
		"unknown-type-local-only-fallback": 1,
		"unknown-type-sdk-compatible":      1,
	}
	for _, issue := range summary.IssueCounts {
		if wantIssues[issue.Issue] != issue.Rows {
			t.Fatalf("IssueCounts contains %q=%d, want %#v", issue.Issue, issue.Rows, wantIssues)
		}
		delete(wantIssues, issue.Issue)
	}
	if len(wantIssues) != 0 {
		t.Fatalf("IssueCounts missing issues: %#v", wantIssues)
	}

	if summary.Samples[0].Issue != "unknown-type-sdk-compatible" || !summary.Samples[0].SDKCompatible || !summary.Samples[0].LocalCompatible {
		t.Fatalf("Samples[0] = %#v, want unknown-type-sdk-compatible sample", summary.Samples[0])
	}
	if summary.Samples[1].Issue != "unknown-type-local-only-fallback" || summary.Samples[1].TimestampKind != "number" || summary.Samples[1].DataKind != "string" {
		t.Fatalf("Samples[1] = %#v, want numeric timestamp scalar fallback sample", summary.Samples[1])
	}
	if summary.Samples[2].Issue != "known-type-local-only-fallback" || !summary.Samples[2].SDKKnownType {
		t.Fatalf("Samples[2] = %#v, want known-type local fallback sample", summary.Samples[2])
	}
}
