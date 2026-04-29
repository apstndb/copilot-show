package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/apstndb/copilot-show/pkg/analyze"
	"github.com/apstndb/copilot-show/pkg/modeldocs"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/spf13/cobra"
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

func TestShortenHomePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		home string
		want string
	}{
		{name: "empty path", path: "", home: "/Users/apstndb", want: "-"},
		{name: "exact home", path: "/Users/apstndb", home: "/Users/apstndb", want: "~"},
		{name: "child path", path: "/Users/apstndb/work/copilot-show", home: "/Users/apstndb", want: "~/work/copilot-show"},
		{name: "outside home", path: "/tmp/copilot-show", home: "/Users/apstndb", want: "/tmp/copilot-show"},
		{name: "empty home", path: "/Users/apstndb/work/copilot-show", home: "", want: "/Users/apstndb/work/copilot-show"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shortenHomePath(tc.path, tc.home); got != tc.want {
				t.Fatalf("shortenHomePath(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
			}
		})
	}
}

func TestFormatRelativeTimestampShort(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: "-"},
		{name: "invalid", raw: "not-a-time", want: "not-a-time"},
		{name: "now", raw: now.Format(time.RFC3339), want: "now"},
		{name: "minutes", raw: now.Add(-45 * time.Minute).Format(time.RFC3339), want: "45m"},
		{name: "hours", raw: now.Add(-5 * time.Hour).Format(time.RFC3339), want: "5h"},
		{name: "days", raw: now.Add(-72 * time.Hour).Format(time.RFC3339), want: "3d"},
		{name: "months", raw: now.Add(-90 * 24 * time.Hour).Format(time.RFC3339), want: "3mo"},
		{name: "years", raw: now.Add(-800 * 24 * time.Hour).Format(time.RFC3339), want: "2y"},
		{name: "future", raw: now.Add(2 * time.Hour).Format(time.RFC3339), want: "in 2h"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatRelativeTimestampShort(tc.raw, now); got != tc.want {
				t.Fatalf("formatRelativeTimestampShort(%q, %s) = %q, want %q", tc.raw, now.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}

func TestFormatSessionInspectionHelpers(t *testing.T) {
	t.Parallel()

	authType := rpc.AuthInfoTypeAPIKey
	reasoning := int64(42)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "optional string nil", got: formatOptionalString(nil), want: "-"},
		{name: "optional string empty", got: formatOptionalString(strPtr("")), want: "-"},
		{name: "optional string value", got: formatOptionalString(strPtr("octocat")), want: "octocat"},
		{name: "auth type nil", got: formatAuthInfoType(nil), want: "-"},
		{name: "auth type value", got: formatAuthInfoType(&authType), want: "api-key"},
		{name: "optional int nil", got: formatOptionalInt64(nil), want: "-"},
		{name: "optional int value", got: formatOptionalInt64(&reasoning), want: "42"},
		{name: "unix millis zero", got: formatUnixMillis(0), want: "-"},
		{name: "unix millis value", got: formatUnixMillis(1713441600000), want: time.UnixMilli(1713441600000).Local().Format(time.RFC3339)},
		{name: "milliseconds zero", got: formatMilliseconds(0), want: "0 ms"},
		{name: "milliseconds rounded", got: formatMilliseconds(1234.4), want: "1234 ms"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Fatalf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

func TestSessionTableWrapWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		maxWidth int
		want     sessionTableWidths
	}{
		{name: "disabled", maxWidth: 0, want: sessionTableWidths{}},
		{name: "narrow baseline", maxWidth: 57, want: sessionTableWidths{id: 7, summary: 10, cwd: 8, status: 10}},
		{name: "eighty column table", maxWidth: 78, want: sessionTableWidths{id: 7, summary: 20, cwd: 14, status: 15}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sessionTableWrapWidths(tc.maxWidth); got != tc.want {
				t.Fatalf("sessionTableWrapWidths(%d) = %+v, want %+v", tc.maxWidth, got, tc.want)
			}
		})
	}
}

func TestShortenSessionTableID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "empty", id: "", want: ""},
		{name: "short", id: "abc123", want: "abc123"},
		{name: "exact", id: "80a6555", want: "80a6555"},
		{name: "uuid", id: "80a65557-c06f-47b7-96cb-78a372de4d9c", want: "80a6555"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shortenSessionTableID(tc.id); got != tc.want {
				t.Fatalf("shortenSessionTableID(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

func TestWrapSessionTableValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		width int
		want  string
	}{
		{name: "disabled width", value: "abcdef", width: 0, want: "abcdef"},
		{name: "single line", value: "abcdefghij", width: 4, want: "abcd\nefgh\nij"},
		{name: "preserve line breaks", value: "abcdef\n12345", width: 3, want: "abc\ndef\n123\n45"},
		{name: "empty line", value: "abc\n\n123", width: 2, want: "ab\nc\n\n12\n3"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := wrapSessionTableValue(tc.value, tc.width); got != tc.want {
				t.Fatalf("wrapSessionTableValue(%q, %d) = %q, want %q", tc.value, tc.width, got, tc.want)
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
	vision := true
	reasoning := true

	got := buildRuntimeModelsSnapshot([]copilot.ModelInfo{
		{
			ID:   "claude-haiku-4.5",
			Name: "Claude Haiku 4.5",
			Billing: &copilot.ModelBilling{
				Multiplier: liveDocsMultiplier,
			},
			Capabilities: copilot.ModelCapabilities{
				Limits: copilot.ModelLimits{
					MaxContextWindowTokens: 200000,
					MaxPromptTokens:        intPtr(8192),
				},
				Supports: copilot.ModelSupports{
					Vision:          vision,
					ReasoningEffort: reasoning,
				},
			},
			DefaultReasoningEffort:    "medium",
			SupportedReasoningEfforts: []string{"low", "medium", "high"},
			Policy: &copilot.ModelPolicy{
				State: "enabled",
			},
		},
		{
			ID:   "custom-runtime",
			Name: "Custom Runtime",
			Billing: &copilot.ModelBilling{
				Multiplier: liveOnlyMultiplier,
			},
			Capabilities: copilot.ModelCapabilities{
				Limits: copilot.ModelLimits{
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

func intPtr(value int) *int {
	return &value
}

func strPtr(value string) *string {
	return &value
}

func parseTestSessionEvents(t *testing.T, rows []string) []*sessionEvent {
	t.Helper()

	events := make([]*sessionEvent, 0, len(rows))
	for i, row := range rows {
		var raw map[string]any
		if err := json.Unmarshal([]byte(row), &raw); err != nil {
			t.Fatalf("json.Unmarshal(row %d) error = %v", i+1, err)
		}
		ev := parseSessionEvent(raw)
		if ev == nil {
			t.Fatalf("parseSessionEvent(row %d) returned nil", i+1)
		}
		events = append(events, ev)
	}
	return events
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

func TestDescribeHistoryEventSessionRemoteSteerableChanged(t *testing.T) {
	t.Parallel()

	ev := &sessionEvent{
		Type: string(copilot.SessionEventTypeSessionRemoteSteerableChanged),
		Data: map[string]any{
			"remoteSteerable": true,
		},
	}

	label, detail, extraLines := describeHistoryEvent(&historyRenderContext{}, ev)
	if label != "Remote Steering Changed" {
		t.Fatalf("describeHistoryEvent() label = %q, want %q", label, "Remote Steering Changed")
	}
	if detail != "Enabled" {
		t.Fatalf("describeHistoryEvent() detail = %q, want %q", detail, "Enabled")
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
	if !isKnownSDKSessionEventType(copilot.SessionEventTypeSamplingRequested) {
		t.Fatal("isKnownSDKSessionEventType(sampling.requested) = false, want true")
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
	if summary.ResumeRows != 0 || summary.GracefulResumeRows != 0 || summary.ResumeWhileInUseRows != 0 || summary.ResumeFromLastEventRows != 0 || summary.SuspiciousResumeRows != 0 {
		t.Fatalf("resume counters = %d/%d/%d/%d/%d, want all zero", summary.ResumeRows, summary.GracefulResumeRows, summary.ResumeWhileInUseRows, summary.ResumeFromLastEventRows, summary.SuspiciousResumeRows)
	}
	if len(summary.ResumeIssueCounts) != 0 || len(summary.ResumeSamples) != 0 {
		t.Fatalf("resume continuity output = %#v / %#v, want none", summary.ResumeIssueCounts, summary.ResumeSamples)
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

func TestSDKUnmarshalSessionEventPreservesUnknownObjectPayload(t *testing.T) {
	t.Parallel()

	line := []byte(`{"id":"evt-unknown","type":"session.future_notice","timestamp":"2026-03-22T14:32:58Z","data":{"message":"future but object"}}`)
	ev, err := copilot.UnmarshalSessionEvent(line)
	if err != nil {
		t.Fatalf("UnmarshalSessionEvent() error = %v", err)
	}

	if ev.Type != copilot.SessionEventType("session.future_notice") {
		t.Fatalf("UnmarshalSessionEvent() type = %q, want %q", ev.Type, "session.future_notice")
	}

	raw, ok := ev.Data.(*copilot.RawSessionEventData)
	if !ok {
		t.Fatalf("UnmarshalSessionEvent() data type = %T, want *copilot.RawSessionEventData", ev.Data)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw.Raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(raw.Raw) error = %v", err)
	}
	if payload["message"] != "future but object" {
		t.Fatalf("json.Unmarshal(raw.Raw) payload = %#v", payload)
	}
}

func TestValidateSessionEventsAtPathResumeContinuity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	rows := []string{
		`{"id":"evt-1","type":"session.start","timestamp":"2026-03-23T00:00:00Z","data":{"sessionId":"session-1"}}`,
		`{"id":"evt-2","type":"user.message","timestamp":"2026-03-23T00:00:01Z","data":{"content":"hello"}}`,
		`{"id":"evt-3","type":"session.shutdown","timestamp":"2026-03-23T00:00:02Z","data":{"totalPremiumRequests":1}}`,
		`{"id":"evt-4","type":"session.resume","timestamp":"2026-03-23T00:00:03Z","parentId":"evt-3","data":{"resumeTime":"2026-03-23T00:00:03Z","eventCount":3,"selectedModel":"gpt-5.4","reasoningEffort":"xhigh","context":{"cwd":"/tmp","gitRoot":"/tmp"},"alreadyInUse":false}}`,
		`{"id":"evt-5","type":"assistant.turn_end","timestamp":"2026-03-23T00:00:04Z","parentId":"evt-4","data":{"turnId":"1"}}`,
		`{"id":"evt-6","type":"session.resume","timestamp":"2026-03-23T00:10:04Z","parentId":"evt-5","data":{"resumeTime":"2026-03-23T00:10:04Z","eventCount":5,"selectedModel":"gpt-5.4","reasoningEffort":"xhigh","context":{"cwd":"/tmp","gitRoot":"/tmp"},"alreadyInUse":true}}`,
		`{"id":"evt-7","type":"assistant.turn_end","timestamp":"2026-03-23T00:10:05Z","parentId":"evt-6","data":{"turnId":"2"}}`,
		`{"id":"evt-8","type":"session.resume","timestamp":"2026-03-23T00:20:05Z","parentId":"evt-7","data":{"resumeTime":"2026-03-23T00:20:05Z","eventCount":7,"selectedModel":"gpt-5.4","reasoningEffort":"xhigh","context":{"cwd":"/tmp","gitRoot":"/tmp"},"alreadyInUse":false}}`,
		`{"id":"evt-9","type":"session.resume","timestamp":"2026-03-23T00:20:06Z","parentId":"evt-1","data":{"resumeTime":"2026-03-23T00:20:06Z","eventCount":999,"selectedModel":"gpt-5.4","reasoningEffort":"xhigh","context":{"cwd":"/tmp","gitRoot":"/tmp"},"alreadyInUse":false}}`,
	}
	if err := os.WriteFile(eventsPath, []byte(strings.Join(rows, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", eventsPath, err)
	}

	summary, err := validateSessionEventsAtPath("session-1", eventsPath, 10)
	if err != nil {
		t.Fatalf("validateSessionEventsAtPath() error = %v", err)
	}

	if summary.ResumeRows != 4 {
		t.Fatalf("ResumeRows = %d, want 4", summary.ResumeRows)
	}
	if summary.GracefulResumeRows != 1 {
		t.Fatalf("GracefulResumeRows = %d, want 1", summary.GracefulResumeRows)
	}
	if summary.ResumeWhileInUseRows != 1 {
		t.Fatalf("ResumeWhileInUseRows = %d, want 1", summary.ResumeWhileInUseRows)
	}
	if summary.ResumeFromLastEventRows != 1 {
		t.Fatalf("ResumeFromLastEventRows = %d, want 1", summary.ResumeFromLastEventRows)
	}
	if summary.SuspiciousResumeRows != 1 {
		t.Fatalf("SuspiciousResumeRows = %d, want 1", summary.SuspiciousResumeRows)
	}
	if len(summary.ResumeIssueCounts) != 3 {
		t.Fatalf("ResumeIssueCounts len = %d, want 3 (%#v)", len(summary.ResumeIssueCounts), summary.ResumeIssueCounts)
	}
	if len(summary.ResumeSamples) != 3 {
		t.Fatalf("ResumeSamples len = %d, want 3 (%#v)", len(summary.ResumeSamples), summary.ResumeSamples)
	}

	wantResumeIssues := map[string]int{
		"resume-event-count-mismatch": 1,
		"resume-while-in-use":         1,
		"resume-from-last-event":      1,
	}
	for _, issue := range summary.ResumeIssueCounts {
		if wantResumeIssues[issue.Issue] != issue.Rows {
			t.Fatalf("ResumeIssueCounts contains %q=%d, want %#v", issue.Issue, issue.Rows, wantResumeIssues)
		}
		delete(wantResumeIssues, issue.Issue)
	}
	if len(wantResumeIssues) != 0 {
		t.Fatalf("ResumeIssueCounts missing issues: %#v", wantResumeIssues)
	}

	if summary.ResumeSamples[0].Issue != "resume-while-in-use" {
		t.Fatalf("ResumeSamples[0].Issue = %q, want resume-while-in-use", summary.ResumeSamples[0].Issue)
	}
	if summary.ResumeSamples[0].ParentType != "assistant.turn_end" || !summary.ResumeSamples[0].AlreadyInUse || !summary.ResumeSamples[0].EventCountMatches || !summary.ResumeSamples[0].ParentMatchesPreviousEvent || summary.ResumeSamples[0].ParentMatchesPreviousShutdown {
		t.Fatalf("ResumeSamples[0] = %#v, want in-use resume sample", summary.ResumeSamples[0])
	}
	if summary.ResumeSamples[0].GapSeconds != 600 {
		t.Fatalf("ResumeSamples[0].GapSeconds = %d, want 600", summary.ResumeSamples[0].GapSeconds)
	}

	if summary.ResumeSamples[1].Issue != "resume-from-last-event" {
		t.Fatalf("ResumeSamples[1].Issue = %q, want resume-from-last-event", summary.ResumeSamples[1].Issue)
	}
	if summary.ResumeSamples[1].ParentType != "assistant.turn_end" || summary.ResumeSamples[1].AlreadyInUse || !summary.ResumeSamples[1].EventCountMatches {
		t.Fatalf("ResumeSamples[1] = %#v, want non-in-use last-event sample", summary.ResumeSamples[1])
	}

	if summary.ResumeSamples[2].Issue != "resume-event-count-mismatch" {
		t.Fatalf("ResumeSamples[2].Issue = %q, want resume-event-count-mismatch", summary.ResumeSamples[2].Issue)
	}
	if summary.ResumeSamples[2].ParentType != "session.start" || summary.ResumeSamples[2].EventCountMatches {
		t.Fatalf("ResumeSamples[2] = %#v, want mismatched older-parent sample", summary.ResumeSamples[2])
	}
}

func TestBuildSessionResumeBranchReport(t *testing.T) {
	t.Parallel()

	events := parseTestSessionEvents(t, []string{
		`{"id":"evt-1","type":"session.start","timestamp":"2026-03-24T00:00:00Z","data":{"sessionId":"session-1"}}`,
		`{"id":"evt-2","type":"user.message","timestamp":"2026-03-24T00:00:01Z","data":{"interactionId":"i1","content":"hello"}}`,
		`{"id":"evt-3","type":"assistant.turn_start","timestamp":"2026-03-24T00:00:02Z","parentId":"evt-2","data":{"turnId":"1","interactionId":"i1"}}`,
		`{"id":"evt-4","type":"assistant.message","timestamp":"2026-03-24T00:00:03Z","parentId":"evt-3","data":{"interactionId":"i1","content":"hi"}}`,
		`{"id":"evt-5","type":"session.shutdown","timestamp":"2026-03-24T00:00:04Z","data":{"totalPremiumRequests":1}}`,
		`{"id":"evt-6","type":"session.resume","timestamp":"2026-03-24T00:00:05Z","parentId":"evt-5","data":{"eventCount":5,"alreadyInUse":false}}`,
		`{"id":"evt-7","type":"user.message","timestamp":"2026-03-24T00:00:06Z","data":{"interactionId":"i2","content":"branch one"}}`,
		`{"id":"evt-8","type":"assistant.turn_start","timestamp":"2026-03-24T00:00:07Z","parentId":"evt-7","data":{"turnId":"2","interactionId":"i2"}}`,
		`{"id":"evt-9","type":"assistant.message","timestamp":"2026-03-24T00:00:08Z","parentId":"evt-8","data":{"interactionId":"i2","content":"first branch reply"}}`,
		`{"id":"evt-10","type":"assistant.turn_end","timestamp":"2026-03-24T00:00:09Z","parentId":"evt-8","data":{"turnId":"2","interactionId":"i2"}}`,
		`{"id":"evt-11","type":"user.message","timestamp":"2026-03-24T00:00:10Z","data":{"interactionId":"i3","content":"branch two"}}`,
		`{"id":"evt-12","type":"assistant.turn_start","timestamp":"2026-03-24T00:00:11Z","parentId":"evt-11","data":{"turnId":"3","interactionId":"i3"}}`,
		`{"id":"evt-13","type":"assistant.message","timestamp":"2026-03-24T00:00:12Z","parentId":"evt-12","data":{"interactionId":"i3","content":"second branch reply"}}`,
		`{"id":"evt-14","type":"assistant.turn_end","timestamp":"2026-03-24T00:00:13Z","parentId":"evt-12","data":{"turnId":"3","interactionId":"i3"}}`,
		`{"id":"evt-15","type":"session.resume","timestamp":"2026-03-24T00:00:14Z","parentId":"evt-14","data":{"eventCount":14,"alreadyInUse":true}}`,
		`{"id":"evt-16","type":"user.message","timestamp":"2026-03-24T00:00:15Z","data":{"interactionId":"i4","content":"parallel branch"}}`,
		`{"id":"evt-17","type":"assistant.turn_start","timestamp":"2026-03-24T00:00:16Z","parentId":"evt-16","data":{"turnId":"4","interactionId":"i4"}}`,
		`{"id":"evt-18","type":"tool.execution_start","timestamp":"2026-03-24T00:00:17Z","parentId":"evt-17","data":{"toolCallId":"call-1","toolName":"bash","interactionId":"i4"}}`,
		`{"id":"evt-19","type":"user.message","timestamp":"2026-03-24T00:00:18Z","data":{"interactionId":"i5","content":"competing branch"}}`,
		`{"id":"evt-20","type":"tool.execution_complete","timestamp":"2026-03-24T00:00:19Z","parentId":"evt-18","data":{"toolCallId":"call-1","interactionId":"i4","model":"gpt-5.4","success":true}}`,
		`{"id":"evt-21","type":"assistant.turn_end","timestamp":"2026-03-24T00:00:20Z","parentId":"evt-17","data":{"turnId":"4","interactionId":"i4"}}`,
		`{"id":"evt-22","type":"session.resume","timestamp":"2026-03-24T00:00:21Z","parentId":"evt-21","data":{"eventCount":999,"alreadyInUse":false}}`,
		`{"id":"evt-23","type":"session.shutdown","timestamp":"2026-03-24T00:00:22Z","data":{"totalPremiumRequests":2}}`,
	})

	report, err := buildSessionResumeBranchReport("session-1", events)
	if err != nil {
		t.Fatalf("buildSessionResumeBranchReport() error = %v", err)
	}

	if report.ResumeRows != 3 || report.GracefulResumeRows != 1 || report.ResumeWhileInUseRows != 1 || report.ResumeEventCountMismatchRows != 1 {
		t.Fatalf("resume counters = %#v", report)
	}
	if len(report.Branches) != 3 {
		t.Fatalf("Branches len = %d, want 3", len(report.Branches))
	}

	if report.Branches[0].Kind != "graceful" || report.Branches[0].SeedReason != "first-new-interaction" {
		t.Fatalf("Branches[0] = %#v, want graceful first-new-interaction", report.Branches[0])
	}
	if len(report.Branches[0].BranchInteractionIDs) != 2 || report.Branches[0].BranchInteractionIDs[0] != "i2" || report.Branches[0].BranchInteractionIDs[1] != "i3" {
		t.Fatalf("Branches[0].BranchInteractionIDs = %#v, want [i2 i3]", report.Branches[0].BranchInteractionIDs)
	}
	if report.Branches[0].FirstInteractionRow != 7 || report.Branches[0].LastInteractionRow != 14 || report.Branches[0].UserMessages != 2 || report.Branches[0].Turns != 2 || report.Branches[0].ToolCalls != 0 || report.Branches[0].Confidence != "high" {
		t.Fatalf("Branches[0] summary = %#v", report.Branches[0])
	}
	if report.Branches[0].FirstUserText != "branch one" {
		t.Fatalf("Branches[0].FirstUserText = %q, want %q", report.Branches[0].FirstUserText, "branch one")
	}

	if report.Branches[1].Kind != "resume-while-in-use" || report.Branches[1].SeedReason != "first-new-interaction" {
		t.Fatalf("Branches[1] = %#v, want resume-while-in-use first-new-interaction", report.Branches[1])
	}
	if len(report.Branches[1].BranchInteractionIDs) != 1 || report.Branches[1].BranchInteractionIDs[0] != "i4" {
		t.Fatalf("Branches[1].BranchInteractionIDs = %#v, want [i4]", report.Branches[1].BranchInteractionIDs)
	}
	if len(report.Branches[1].CompetingInteractionIDs) != 1 || report.Branches[1].CompetingInteractionIDs[0] != "i5" {
		t.Fatalf("Branches[1].CompetingInteractionIDs = %#v, want [i5]", report.Branches[1].CompetingInteractionIDs)
	}
	if report.Branches[1].ToolCalls != 1 || len(report.Branches[1].Models) != 1 || report.Branches[1].Models[0] != "gpt-5.4" || report.Branches[1].Confidence != "medium" {
		t.Fatalf("Branches[1] tool/model summary = %#v", report.Branches[1])
	}

	if report.Branches[2].Kind != "resume-event-count-mismatch" || report.Branches[2].SeedReason != "no-interaction" || len(report.Branches[2].BranchInteractionIDs) != 0 || report.Branches[2].Confidence != "low" {
		t.Fatalf("Branches[2] = %#v, want mismatch branch without inferred interaction", report.Branches[2])
	}
	if report.Branches[2].NextShutdownRow != 23 {
		t.Fatalf("Branches[2].NextShutdownRow = %d, want 23", report.Branches[2].NextShutdownRow)
	}
}

func TestFormatStatsAPICostDetails(t *testing.T) {
	t.Parallel()

	cacheReadRate := 0.25
	stat := &analyze.ModelStat{
		Input:     3_000_000,
		CacheRead: 2_000_000,
		Output:    1_000_000,
		ExtraUsageTokens: map[string]int64{
			"cacheOutputTokens": 250_000,
		},
		EstimatedAPICost: &analyze.APICostEstimate{
			UncachedInputTokens: 1_000_000,
			InputUSDPerMTok:     2.5,
			CacheReadUSDPerMTok: &cacheReadRate,
			OutputUSDPerMTok:    15,
			InputUSD:            2.5,
			CacheReadUSD:        0.5,
			OutputUSD:           15,
			TotalUSD:            18,
			IsComplete:          true,
		},
	}

	kinds := formatStatsAPICostKinds(stat)
	if !strings.Contains(kinds, "Input Tokens") || !strings.Contains(kinds, "Cache Read Tokens") || !strings.Contains(kinds, "Cache Output Tokens") || !strings.Contains(kinds, "Output Tokens") {
		t.Fatalf("formatStatsAPICostKinds() = %q", kinds)
	}

	tokens := formatStatsAPICostTokenValues(stat)
	if !strings.Contains(tokens, "1000000") || !strings.Contains(tokens, "2000000") || !strings.Contains(tokens, "250000") {
		t.Fatalf("formatStatsAPICostTokenValues() = %q", tokens)
	}
	if strings.Contains(tokens, "3000000") {
		t.Fatalf("formatStatsAPICostTokenValues() = %q, want billed input count only", tokens)
	}

	rates := formatStatsAPICostRates(stat)
	if !strings.Contains(rates, "2.5") || !strings.Contains(rates, "0.25") || !strings.Contains(rates, "15") {
		t.Fatalf("formatStatsAPICostRates() = %q", rates)
	}

	subtotals := formatStatsAPICostSubtotals(stat)
	if !strings.Contains(subtotals, "$2.50") || !strings.Contains(subtotals, "$0.50") || !strings.Contains(subtotals, "$15.00") {
		t.Fatalf("formatStatsAPICostSubtotals() = %q", subtotals)
	}
	if !strings.Contains(subtotals, "-") {
		t.Fatalf("formatStatsAPICostSubtotals() = %q, want placeholder for unpriced extra usage", subtotals)
	}
}

func TestConfigureShowHiddenHelp(t *testing.T) {
	t.Parallel()

	var showHidden bool
	root := &cobra.Command{Use: "root", Short: "root"}
	root.PersistentFlags().BoolVar(&showHidden, "show-hidden", false, "Include hidden commands and hidden flags in help output")
	root.PersistentFlags().Bool("secret", false, "secret global flag")
	if err := root.PersistentFlags().MarkHidden("secret"); err != nil {
		t.Fatalf("MarkHidden(secret) error = %v", err)
	}

	child := &cobra.Command{Use: "child", Short: "child"}
	child.Flags().Bool("local-secret", false, "secret local flag")
	if err := child.Flags().MarkHidden("local-secret"); err != nil {
		t.Fatalf("MarkHidden(local-secret) error = %v", err)
	}
	root.AddCommand(child)
	root.AddCommand(&cobra.Command{Use: "hidden", Short: "hidden", Hidden: true})

	configureShowHiddenHelp(root, &showHidden)

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	if err := root.Help(); err != nil {
		t.Fatalf("root.Help() error = %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "root hidden") {
		t.Fatalf("root.Help() without showHidden unexpectedly included hidden command: %q", out)
	}
	if strings.Contains(out, "--secret") {
		t.Fatalf("root.Help() without showHidden unexpectedly included hidden flag: %q", out)
	}

	showHidden = true
	buf.Reset()
	if err := root.Help(); err != nil {
		t.Fatalf("root.Help() with showHidden error = %v", err)
	}
	out = buf.String()
	if !strings.Contains(out, "root hidden") {
		t.Fatalf("root.Help() with showHidden did not include hidden command: %q", out)
	}
	if !strings.Contains(out, "--secret") {
		t.Fatalf("root.Help() with showHidden did not include hidden flag: %q", out)
	}

	rootSecret := root.PersistentFlags().Lookup("secret")
	childSecret := child.Flags().Lookup("local-secret")
	var hiddenCmd *cobra.Command
	for _, candidate := range root.Commands() {
		if candidate.Name() == "hidden" {
			hiddenCmd = candidate
			break
		}
	}
	if rootSecret == nil || childSecret == nil || hiddenCmd == nil {
		t.Fatalf("expected hidden state to exist, got root=%#v child=%#v hiddenCmd=%#v", rootSecret, childSecret, hiddenCmd)
	}
	if !rootSecret.Hidden || !childSecret.Hidden || !hiddenCmd.Hidden {
		t.Fatalf("expected hidden states before wrapper, got root=%v child=%v cmd=%v", rootSecret.Hidden, childSecret.Hidden, hiddenCmd.Hidden)
	}
	withVisibleHiddenHelp(child, true, func() {
		if rootSecret.Hidden || childSecret.Hidden || hiddenCmd.Hidden {
			t.Fatalf("withVisibleHiddenHelp() did not unhide all states: root=%v child=%v cmd=%v", rootSecret.Hidden, childSecret.Hidden, hiddenCmd.Hidden)
		}
	})
	if !rootSecret.Hidden || !childSecret.Hidden || !hiddenCmd.Hidden {
		t.Fatalf("withVisibleHiddenHelp() did not restore states: root=%v child=%v cmd=%v", rootSecret.Hidden, childSecret.Hidden, hiddenCmd.Hidden)
	}
}
