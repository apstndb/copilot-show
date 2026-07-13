package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/apstndb/copilot-show/pkg/analyze"
	"github.com/apstndb/copilot-show/pkg/modeldocs"
	"github.com/apstndb/copilot-show/pkg/render"
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

func TestCommandNeedsCopilotClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path []string
		want bool
	}{
		{name: "root", path: []string{}, want: false},
		{name: "online command", path: []string{"models"}, want: true},
		{name: "offline command", path: []string{"history"}, want: false},
		{name: "help command", path: []string{"help"}, want: false},
		{name: "completion child", path: []string{"completion", "zsh"}, want: false},
		{name: "completion request", path: []string{cobra.ShellCompRequestCmd}, want: false},
		{name: "completion request without descriptions", path: []string{cobra.ShellCompNoDescRequestCmd}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := &cobra.Command{Use: "copilot-show"}
			current := root
			for _, name := range test.path {
				child := &cobra.Command{Use: name}
				current.AddCommand(child)
				current = child
			}
			if got := commandNeedsCopilotClient(current); got != test.want {
				t.Fatalf(
					"commandNeedsCopilotClient(%q) = %v, want %v",
					current.CommandPath(),
					got,
					test.want,
				)
			}
		})
	}
}

func TestCopilotClientStartupCobraLifecycle(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantStarts    int
		wantQuotaRuns int
	}{
		{name: "root without arguments", args: []string{}, wantStarts: 0},
		{name: "root version", args: []string{"--version"}, wantStarts: 0},
		{name: "online command", args: []string{"quota"}, wantStarts: 1, wantQuotaRuns: 1},
		{name: "online command help", args: []string{"quota", "--help"}, wantStarts: 0},
		{name: "online command explicit false help", args: []string{"quota", "--help=false"}, wantStarts: 1, wantQuotaRuns: 1},
		{name: "help command", args: []string{"help", "quota"}, wantStarts: 0},
		{name: "completion command", args: []string{"completion", "zsh"}, wantStarts: 0},
		{name: "completion request", args: []string{cobra.ShellCompRequestCmd, "quota", ""}, wantStarts: 0},
		{name: "completion request without descriptions", args: []string{cobra.ShellCompNoDescRequestCmd, "quota", ""}, wantStarts: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			starts := 0
			quotaRuns := 0

			copilotClientStartMu.Lock()
			originalStart := startCopilotClient
			originalStarted := copilotClientStarted
			originalStartErr := copilotClientStartErr
			startCopilotClient = func(context.Context, *copilot.Client) error {
				starts++
				return nil
			}
			copilotClientStarted = false
			copilotClientStartErr = nil
			copilotClientStartMu.Unlock()
			t.Cleanup(func() {
				copilotClientStartMu.Lock()
				defer copilotClientStartMu.Unlock()
				startCopilotClient = originalStart
				copilotClientStarted = originalStarted
				copilotClientStartErr = originalStartErr
			})

			root := &cobra.Command{
				Use:           "copilot-show",
				Version:       "test",
				SilenceErrors: true,
				SilenceUsage:  true,
				PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
					if !commandNeedsCopilotClient(cmd) {
						return nil
					}
					return ensureCopilotClient(cmd.Context(), nil)
				},
				Run: func(*cobra.Command, []string) {},
			}
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(test.args)
			root.AddCommand(&cobra.Command{
				Use: "quota",
				Run: func(*cobra.Command, []string) {
					quotaRuns++
				},
			})

			if err := root.Execute(); err != nil {
				t.Fatalf("Execute(%q) error = %v", test.args, err)
			}
			if starts != test.wantStarts {
				t.Errorf("Execute(%q) starts = %d, want %d", test.args, starts, test.wantStarts)
			}
			if quotaRuns != test.wantQuotaRuns {
				t.Errorf("Execute(%q) quota runs = %d, want %d", test.args, quotaRuns, test.wantQuotaRuns)
			}
		})
	}
}

func TestUsageCommandRejectsInvalidEnumValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "billing",
			args:    []string{"--billing", "invalid"},
			wantErr: "invalid --billing",
		},
		{
			name:    "sort order",
			args:    []string{"--sort-order", "sideways"},
			wantErr: "invalid --sort-order",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cmd := newUsageCmd(nil)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(test.args)

			err := cmd.Execute()
			if err == nil {
				t.Fatalf("Execute(%q) error = nil, want error", test.args)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Execute(%q) error = %q, want substring %q", test.args, err, test.wantErr)
			}
		})
	}
}

func TestResolveOfflineSessionID(t *testing.T) {
	baseTime := time.Date(
		2026,
		7,
		13,
		0,
		0,
		0,
		0,
		time.UTC,
	)
	tests := []struct {
		name     string
		args     []string
		sessions map[string]time.Time
		want     string
		wantErr  bool
	}{
		{
			name: "newest local log wins when omitted",
			args: []string{},
			sessions: map[string]time.Time{
				"foreground-session":   baseTime,
				"newest-local-session": baseTime.Add(time.Hour),
			},
			want: "newest-local-session",
		},
		{
			name: "explicit ID wins over newer local log",
			args: []string{"foreground-session"},
			sessions: map[string]time.Time{
				"foreground-session":   baseTime,
				"newest-local-session": baseTime.Add(time.Hour),
			},
			want: "foreground-session",
		},
		{
			name:     "no local session",
			args:     []string{},
			sessions: map[string]time.Time{},
			wantErr:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			for sessionID, modifiedAt := range test.sessions {
				eventsPath := filepath.Join(
					home,
					".copilot",
					"session-state",
					sessionID,
					"events.jsonl",
				)
				if err := os.MkdirAll(filepath.Dir(eventsPath), 0o755); err != nil {
					t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(eventsPath), err)
				}
				if err := os.WriteFile(eventsPath, nil, 0o644); err != nil {
					t.Fatalf("WriteFile(%q) error = %v", eventsPath, err)
				}
				if err := os.Chtimes(eventsPath, modifiedAt, modifiedAt); err != nil {
					t.Fatalf("Chtimes(%q) error = %v", eventsPath, err)
				}
			}

			got, err := resolveOfflineSessionID(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatalf("resolveOfflineSessionID(%q) error = nil, want error", test.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveOfflineSessionID(%q) error = %v", test.args, err)
			}
			if got != test.want {
				t.Fatalf(
					"resolveOfflineSessionID(%q) = %q, want %q",
					test.args,
					got,
					test.want,
				)
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

	inputPrice := 2.5
	outputPrice := 15.0
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
				VisibleNow: true,
				LiveModels: []modeldocs.LiveMatch{
					{
						ID:   "gpt-5.4",
						Name: "GPT-5.4",
						TokenPricing: &modeldocs.SDKTokenPricing{
							InputUSDPerMTok:  &inputPrice,
							OutputUSDPerMTok: &outputPrice,
						},
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

	got := buildCLIFocusedModelDocsSnapshot(snapshot, false)
	if got.CatalogVersion != snapshot.CatalogVersion || got.SourceNote != snapshot.SourceNote || got.LoadedFrom != snapshot.LoadedFrom {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() did not preserve top-level metadata: %#v", got)
	}
	if len(got.Models) != 2 {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() models len = %d, want 2", len(got.Models))
	}
	if got.Models[0].Provider != "OpenAI" || got.Models[0].ReleaseStatus != "GA" || !got.Models[0].VisibleNow {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() model = %#v", got.Models[0])
	}
	if got.Models[0].TokenPricing == nil || got.Models[0].TokenPricing.InputUSDPerMTok == nil || *got.Models[0].TokenPricing.InputUSDPerMTok != inputPrice {
		t.Fatalf("buildCLIFocusedModelDocsSnapshot() token pricing = %#v", got.Models[0].TokenPricing)
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

func TestFormatReasoningEffortsForTableMarksDefault(t *testing.T) {
	t.Parallel()

	if got := formatReasoningEffortsForTable([]string{"low", "medium", "high"}, "medium"); got != "low, medium*, high" {
		t.Fatalf("formatReasoningEffortsForTable() = %q, want default marked with asterisk", got)
	}
}

func TestFormatTokenCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value int
		want  string
	}{
		{value: 0, want: "-"},
		{value: 8192, want: "8192"},
		{value: 128000, want: "128k"},
		{value: 936000, want: "936k"},
		{value: 1000000, want: "1M"},
		{value: 2000000, want: "2M"},
	}
	for _, tt := range tests {
		if got := formatTokenCount(tt.value); got != tt.want {
			t.Fatalf("formatTokenCount(%d) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestModelsTableLayoutDefaultShowsAPIPricingOnly(t *testing.T) {
	t.Parallel()

	uiVersion = uiVersionNew
	t.Cleanup(func() { uiVersion = uiVersionNew })

	header, rightAligned := modelsTableLayout()
	if len(header) != 9 || header[2] != modelsAPIPricingColumnHeader {
		t.Fatalf("modelsTableLayout() header = %#v, want %q in third column", header, modelsAPIPricingColumnHeader)
	}
	if len(rightAligned) != 2 || rightAligned[0] != 3 || rightAligned[1] != 4 {
		t.Fatalf("modelsTableLayout() rightAligned = %#v, want [3 4]", rightAligned)
	}

	row := modelsTableRow(runtimeModelView{
		ID:   "gpt-5.4",
		Name: "GPT-5.4",
		TokenPricing: &modeldocs.SDKTokenPricing{
			InputUSDPerMTok:  float64Ptr(2.5),
			OutputUSDPerMTok: float64Ptr(15),
		},
		MaxContextWindowTokens: 272000,
	})
	if len(row) != len(header) || row[2] != "2.5/15" {
		t.Fatalf("modelsTableRow() = %#v, want %q pricing in third column", row, modelsAPIPricingColumnHeader)
	}
}

func float64Ptr(v float64) *float64 {
	return &v
}

func TestBuildRuntimeModelsSnapshotUsesSDKTokenPricing(t *testing.T) {
	t.Parallel()

	priceInput := 1.0
	liveDocsMultiplier := 9.0
	vision := true
	reasoning := true

	got := buildRuntimeModelsSnapshot([]copilot.ModelInfo{
		{
			ID:   "claude-haiku-4.5",
			Name: "Claude Haiku 4.5",
			Billing: &copilot.ModelBilling{
				Multiplier: &liveDocsMultiplier,
				TokenPrices: &rpc.ModelBillingTokenPrices{
					InputPrice:  float64Ptr(100),
					OutputPrice: float64Ptr(500),
				},
			},
			Capabilities: copilot.ModelCapabilities{
				Limits: copilot.ModelLimits{
					MaxContextWindowTokens: intPtr(200000),
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
				Terms: "preview",
			},
		},
		{
			ID:   "custom-runtime",
			Name: "Custom Runtime",
			Capabilities: copilot.ModelCapabilities{
				Limits: copilot.ModelLimits{
					MaxContextWindowTokens: intPtr(100000),
				},
			},
		},
	}, modeldocs.Snapshot{
		CatalogVersion: "catalog",
		LoadedFrom:     "embedded",
		LoadWarnings:   []string{"warn"},
	})

	if got.ModelCatalogVersion != "catalog" || got.ModelCatalogLoadedFrom != "embedded" {
		t.Fatalf("buildRuntimeModelsSnapshot() metadata = %#v", got)
	}
	if len(got.Models) != 2 {
		t.Fatalf("buildRuntimeModelsSnapshot() models len = %d, want 2", len(got.Models))
	}
	if got.Models[0].TokenPricing == nil || got.Models[0].TokenPricing.InputUSDPerMTok == nil || *got.Models[0].TokenPricing.InputUSDPerMTok != priceInput {
		t.Fatalf("buildRuntimeModelsSnapshot() token pricing = %#v", got.Models[0].TokenPricing)
	}
	if !got.Models[0].SupportsVision || !got.Models[0].SupportsReasoning || got.Models[0].DefaultReasoningEffort != "medium" {
		t.Fatalf("buildRuntimeModelsSnapshot() first model view = %#v", got.Models[0])
	}
	if got.Models[0].State != "enabled" || got.Models[0].PolicyTerms != "preview" {
		t.Fatalf("buildRuntimeModelsSnapshot() policy = state %q terms %q", got.Models[0].State, got.Models[0].PolicyTerms)
	}
	if got.Models[0].BillingMultiplier == nil || *got.Models[0].BillingMultiplier != liveDocsMultiplier {
		t.Fatalf("buildRuntimeModelsSnapshot() billing multiplier = %#v, want %v", got.Models[0].BillingMultiplier, liveDocsMultiplier)
	}
	if got.Models[1].TokenPricing != nil {
		t.Fatalf("buildRuntimeModelsSnapshot() runtime-only token pricing = %#v, want nil", got.Models[1].TokenPricing)
	}
}

func TestAuthInfoViewRedactsSecrets(t *testing.T) {
	t.Parallel()

	got := authInfoView(&rpc.GhCLIAuthInfo{
		Login: "octocat",
		Host:  "https://github.com",
		Token: "secret-token",
		CopilotUser: &rpc.CopilotUserResponse{
			CopilotPlan: strPtr("individual_pro"),
		},
	})
	if got.Type != string(rpc.AuthInfoTypeGhCLI) {
		t.Fatalf("authInfoView().Type = %q, want gh-cli", got.Type)
	}
	if got.Login != "octocat" || got.Host != "https://github.com" || got.CopilotPlan != "individual_pro" {
		t.Fatalf("authInfoView() = %#v", got)
	}
	if !got.HasSecret {
		t.Fatalf("authInfoView() HasSecret = false, want true")
	}
}

func TestSummarizeMCPServerConfig(t *testing.T) {
	t.Parallel()

	stdio := summarizeMCPServerConfig("local", &rpc.MCPServerConfigStdio{
		Command: "npx",
		Args:    []string{"-y", "server"},
	})
	if stdio.Transport != "stdio" || stdio.Target != "npx -y server" {
		t.Fatalf("summarizeMCPServerConfig(stdio) = %#v", stdio)
	}

	httpType := rpc.MCPServerConfigHTTPType("sse")
	http := summarizeMCPServerConfig("remote", &rpc.MCPServerConfigHTTP{
		URL:  "https://example.com/mcp",
		Type: &httpType,
	})
	if http.Transport != "sse" || http.Target != "https://example.com/mcp" {
		t.Fatalf("summarizeMCPServerConfig(http) = %#v", http)
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

	knownTypes := []copilot.SessionEventType{
		copilot.SessionEventTypeUserMessage,
		copilot.SessionEventTypeSamplingRequested,
		copilot.SessionEventTypeAssistantMessageStart,
		copilot.SessionEventTypeSessionTodosChanged,
		copilot.SessionEventTypeAssistantIdle,
		copilot.SessionEventTypeMCPHeadersRefreshCompleted,
		copilot.SessionEventTypeMCPHeadersRefreshRequired,
		copilot.SessionEventTypeSessionLimitsExhaustedCompleted,
		copilot.SessionEventTypeSessionLimitsExhaustedRequested,
		copilot.SessionEventTypeSessionSessionLimitsChanged,
		copilot.SessionEventTypeSessionUsageCheckpoint,
	}
	for _, eventType := range knownTypes {
		if !isKnownSDKSessionEventType(eventType) {
			t.Fatalf("isKnownSDKSessionEventType(%q) = false, want true", eventType)
		}
	}
	if isKnownSDKSessionEventType(copilot.SessionEventType("session.future_notice")) {
		t.Fatal("isKnownSDKSessionEventType(session.future_notice) = true, want false")
	}
}

func TestFormatVisionSupport(t *testing.T) {
	t.Parallel()
	render.SetTableFoldEnabled(false)
	t.Cleanup(func() { render.SetTableFoldEnabled(true) })

	tests := []struct {
		name      string
		supported bool
		limits    *copilot.ModelVisionLimits
		want      string
	}{
		{name: "unsupported", supported: false, want: "No"},
		{name: "supported without limits", supported: true, want: "Yes"},
		{
			name:      "supported with limits",
			supported: true,
			limits: &copilot.ModelVisionLimits{
				MaxPromptImages:     3,
				MaxPromptImageSize:  5 * 1024 * 1024,
				SupportedMediaTypes: []string{"image/png", "image/jpeg"},
			},
			want: "Yes (3 images, 5.0 MiB)",
		},
		{
			name:      "single image",
			supported: true,
			limits: &copilot.ModelVisionLimits{
				MaxPromptImages: 1,
			},
			want: "Yes (1 image)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := formatVisionSupport(tt.supported, tt.limits); got != tt.want {
				t.Fatalf("formatVisionSupport() = %q, want %q", got, tt.want)
			}
		})
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
	ev, err := unmarshalSessionEvent(line)
	if err != nil {
		t.Fatalf("unmarshalSessionEvent() error = %v", err)
	}

	if ev.Type() != copilot.SessionEventType("session.future_notice") {
		t.Fatalf("unmarshalSessionEvent() type = %q, want %q", ev.Type(), "session.future_notice")
	}

	raw, ok := ev.Data.(*copilot.RawSessionEventData)
	if !ok {
		t.Fatalf("unmarshalSessionEvent() data type = %T, want *copilot.RawSessionEventData", ev.Data)
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

func TestCreateSessionConfigDiscoverFlag(t *testing.T) {
	t.Parallel()

	orig := enableConfigDiscovery
	t.Cleanup(func() {
		enableConfigDiscovery = orig
	})

	enableConfigDiscovery = false
	cfg := createSessionConfig()
	if cfg.EnableConfigDiscovery != nil {
		t.Fatalf("expected EnableConfigDiscovery to be unset when --discover is off, got %v", *cfg.EnableConfigDiscovery)
	}
	if cfg.ClientName != copilotSDKClientName {
		t.Fatalf("ClientName = %q, want %q", cfg.ClientName, copilotSDKClientName)
	}

	enableConfigDiscovery = true
	cfg = createSessionConfig()
	if cfg.EnableConfigDiscovery == nil || !*cfg.EnableConfigDiscovery {
		t.Fatalf("expected EnableConfigDiscovery=true when --discover is on, got %#v", cfg.EnableConfigDiscovery)
	}
}

func TestResumeSessionConfigDiscoverFlag(t *testing.T) {
	t.Parallel()

	orig := enableConfigDiscovery
	t.Cleanup(func() {
		enableConfigDiscovery = orig
	})

	enableConfigDiscovery = true
	cfg := resumeSessionConfig()
	if cfg.EnableConfigDiscovery == nil || !*cfg.EnableConfigDiscovery {
		t.Fatalf("expected EnableConfigDiscovery=true when --discover is on, got %#v", cfg.EnableConfigDiscovery)
	}
	if !cfg.SuppressResumeEvent {
		t.Fatal("expected SuppressResumeEvent to remain enabled for resumed diagnostics sessions")
	}
}

func TestParseUsageBillingMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw   string
		want  string
		isErr bool
	}{
		{raw: "", want: usageBillingPremiumRequest},
		{raw: "premium-request", want: usageBillingPremiumRequest},
		{raw: "ai-credits", want: usageBillingAICredits},
		{raw: "auto", want: usageBillingAuto},
		{raw: "invalid", isErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got, err := parseUsageBillingMode(tc.raw)
			if tc.isErr {
				if err == nil {
					t.Fatal("parseUsageBillingMode() expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUsageBillingMode() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseUsageBillingMode(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestBuildUsageAPIPath(t *testing.T) {
	t.Parallel()

	got := buildUsageAPIPath("octocat", 2026, 6, 15, "copilot", "gpt-5", usageBillingAICredits)
	want := "/users/octocat/settings/billing/ai_credit/usage?day=15&model=gpt-5&month=6&product=copilot&year=2026"
	if got != want {
		t.Fatalf("buildUsageAPIPath(ai-credits) = %q, want %q", got, want)
	}

	got = buildUsageAPIPath("octocat", 2026, 0, 0, "", "", usageBillingPremiumRequest)
	want = "/users/octocat/settings/billing/premium_request/usage?year=2026"
	if got != want {
		t.Fatalf("buildUsageAPIPath(premium-request) = %q, want %q", got, want)
	}
}

func TestUsageQuantityColumnHeaders(t *testing.T) {
	t.Parallel()

	used, billed := usageQuantityColumnHeaders(usageBillingPremiumRequest)
	if used != "Used (req.)" || billed != "Billed (req.)" {
		t.Fatalf("premium headers = %q / %q", used, billed)
	}

	used, billed = usageQuantityColumnHeaders(usageBillingAICredits)
	if used != "Used (credits)" || billed != "Billed (credits)" {
		t.Fatalf("ai-credits headers = %q / %q", used, billed)
	}
}

func TestResolveUsageBillingMode(t *testing.T) {
	t.Parallel()

	premium := &usageResponse{UsageItems: []usageItem{{Model: "gpt-5"}}}
	aiCredits := &usageResponse{UsageItems: []usageItem{{UnitType: "ai-credits"}}}
	quota := &rpc.AccountGetQuotaResult{
		QuotaSnapshots: map[string]rpc.AccountQuotaSnapshot{
			"ai_credits": {EntitlementRequests: 20000},
		},
	}

	if got := resolveUsageBillingMode(usageBillingAICredits, nil, nil, nil); got != usageBillingAICredits {
		t.Fatalf("explicit ai-credits = %q", got)
	}
	if got := resolveUsageBillingMode(usageBillingAuto, premium, aiCredits, quota); got != usageBillingAICredits {
		t.Fatalf("auto with ai_credits quota snapshot = %q, want ai-credits", got)
	}
	if got := resolveUsageBillingMode(usageBillingAuto, premium, aiCredits, nil); got != usageBillingPremiumRequest {
		t.Fatalf("auto with both datasets = %q, want premium-request", got)
	}
	if got := resolveUsageBillingMode(usageBillingAuto, nil, aiCredits, nil); got != usageBillingAICredits {
		t.Fatalf("auto with only ai-credits data = %q", got)
	}
}

func TestNanoAiuToCredits(t *testing.T) {
	t.Parallel()

	if got := nanoAiuToCredits(1_500_000_000); got != 1.5 {
		t.Fatalf("nanoAiuToCredits() = %v, want 1.5", got)
	}
	if got := formatOptionalCredits(nil); got != "-" {
		t.Fatalf("formatOptionalCredits(nil) = %q", got)
	}
	value := 754.52 * nanoAiuPerCredit
	if got := formatOptionalCredits(&value); got != "754.52" {
		t.Fatalf("formatOptionalCredits(754.52 credits) = %q", got)
	}
}

func TestQuotaIncludedLimit(t *testing.T) {
	t.Parallel()

	quota := &rpc.AccountGetQuotaResult{
		QuotaSnapshots: map[string]rpc.AccountQuotaSnapshot{
			"premium_interactions": {EntitlementRequests: 300},
			"ai_credits":           {EntitlementRequests: 20000},
		},
	}

	if got := quotaIncludedLimit(quota, usageBillingPremiumRequest); got != 300 {
		t.Fatalf("premium included = %v", got)
	}
	if got := quotaIncludedLimit(quota, usageBillingAICredits); got != 20000 {
		t.Fatalf("ai credits included = %v", got)
	}
}

func TestUsageResponseJSON(t *testing.T) {
	t.Parallel()

	raw := `{
		"timePeriod": {"year": 2026, "month": 6},
		"user": "octocat",
		"usageItems": [{
			"product": "copilot",
			"sku": "copilot-chat",
			"model": "gpt-5",
			"unitType": "ai-credits",
			"pricePerUnit": 0.01,
			"grossQuantity": 754.52,
			"netQuantity": 0,
			"netAmount": 0
		}]
	}`

	var res usageResponse
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(res.UsageItems) != 1 || res.UsageItems[0].UnitType != "ai-credits" {
		t.Fatalf("parsed usage response = %#v", res)
	}
	if res.UsageItems[0].GrossQuantity != 754.52 {
		t.Fatalf("grossQuantity = %v", res.UsageItems[0].GrossQuantity)
	}
}
