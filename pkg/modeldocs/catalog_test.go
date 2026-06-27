package modeldocs

import (
	"context"
	"fmt"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func float64Ptr(v float64) *float64 {
	return &v
}

func TestNormalizeModelNameKey(t *testing.T) {
	tests := []struct {
		a string
		b string
	}{
		{"Gemini 3 Pro (Preview)", "gemini-3-pro-preview"},
		{"Claude Sonnet 4.0", "claude-sonnet-4"},
		{"GPT-5.1 Codex Max", "gpt-5.1-codex-max"},
		{"GPT-4.1[^1]", "gpt-4.1"},
	}

	for _, tt := range tests {
		if gotA, gotB := NormalizeModelNameKey(tt.a), NormalizeModelNameKey(tt.b); gotA != gotB {
			t.Fatalf("NormalizeModelNameKey(%q) = %q, NormalizeModelNameKey(%q) = %q", tt.a, gotA, tt.b, gotB)
		}
	}
}

func TestBuildSnapshotMatchesPreviewNames(t *testing.T) {
	snapshot, err := BuildSnapshotWithOptions(context.Background(), []copilot.ModelInfo{
		{
			ID:   "gemini-3.1-pro-preview",
			Name: "Gemini 3.1 Pro (Preview)",
			Policy: &copilot.ModelPolicy{
				State: "enabled",
			},
			Billing: &copilot.ModelBilling{
				Multiplier: float64Ptr(1),
				TokenPrices: &rpc.ModelBillingTokenPrices{
					InputPrice:  float64Ptr(250),
					OutputPrice: float64Ptr(1500),
				},
			},
		},
	}, SnapshotOptions{})
	if err != nil {
		t.Fatalf("BuildSnapshotWithOptions() error = %v", err)
	}

	var gemini31 JoinedModel
	var gpt5Mini JoinedModel
	foundGemini31 := false
	foundGPT5Mini := false
	for _, model := range snapshot.Models {
		switch model.Name {
		case "Gemini 3.1 Pro":
			gemini31 = model
			foundGemini31 = true
		case "GPT-5 mini":
			gpt5Mini = model
			foundGPT5Mini = true
		}
	}

	if !foundGemini31 {
		t.Fatalf("Gemini 3.1 Pro docs row not found")
	}
	if gemini31.Provider != "Google" {
		t.Fatalf("Gemini 3.1 Pro provider = %q, want %q", gemini31.Provider, "Google")
	}
	if !gemini31.VisibleNow {
		t.Fatalf("Gemini 3.1 Pro should be matched as visible")
	}
	if len(gemini31.LiveModels) != 1 || gemini31.LiveModels[0].ID != "gemini-3.1-pro-preview" {
		t.Fatalf("Gemini 3.1 Pro live matches = %#v, want gemini-3.1-pro-preview", gemini31.LiveModels)
	}
	if gemini31.LiveModels[0].TokenPricing == nil || gemini31.LiveModels[0].TokenPricing.InputUSDPerMTok == nil || *gemini31.LiveModels[0].TokenPricing.InputUSDPerMTok != 2.5 {
		t.Fatalf("Gemini 3.1 Pro token pricing = %#v, want input=2.5", gemini31.LiveModels[0].TokenPricing)
	}
	if !foundGPT5Mini {
		t.Fatalf("GPT-5 mini docs row not found")
	}
	if !gpt5Mini.Plans.Pro || !gpt5Mini.Plans.Max {
		t.Fatalf("GPT-5 mini plans = %#v, want Pro and Max", gpt5Mini.Plans)
	}

	if snapshot.LoadedFrom != string(loadModeEmbedded) {
		t.Fatalf("LoadedFrom = %q, want %q", snapshot.LoadedFrom, loadModeEmbedded)
	}
	if !strings.HasPrefix(snapshot.CatalogVersion, "github-docs-snapshot-") {
		t.Fatalf("CatalogVersion = %q, want github-docs-snapshot-*", snapshot.CatalogVersion)
	}
	if len(snapshot.LiveModelsWithoutDocs) != 0 {
		t.Fatalf("LiveModelsWithoutDocs = %#v, want empty", snapshot.LiveModelsWithoutDocs)
	}
}

func TestBuildSnapshotWithLatestFallback(t *testing.T) {
	fetcher := func(_ context.Context, url string) ([]byte, error) {
		switch {
		case strings.HasSuffix(url, "/model-release-status.yml"):
			return []byte("- name: Gemini 3 Pro\n  release_status: Public preview\n"), nil
		case strings.HasSuffix(url, "/model-supported-clients.yml"):
			return []byte("[]\n"), nil
		case strings.HasSuffix(url, "/model-supported-plans.yml"):
			return []byte("- name: Gemini 3 Pro\n  pro: true\n  pro_plus: true\n  max: true\n  business: true\n  enterprise: true\n"), nil
		case strings.HasSuffix(url, "/model-comparison.yml"):
			return []byte("[]\n"), nil
		case strings.HasSuffix(url, "/model-deprecation-history.yml"):
			return []byte("[]\n"), nil
		default:
			return nil, fmt.Errorf("unexpected url %s", url)
		}
	}

	snapshot, err := buildSnapshotWithFetcher(context.Background(), []copilot.ModelInfo{{ID: "gemini-3-pro-preview", Name: "Gemini 3 Pro (Preview)"}}, SnapshotOptions{PreferLatest: true}, fetcher)
	if err != nil {
		t.Fatalf("buildSnapshotWithFetcher() error = %v", err)
	}

	if snapshot.LoadedFrom != string(loadModeEmbeddedFallback) {
		t.Fatalf("LoadedFrom = %q, want %q", snapshot.LoadedFrom, loadModeEmbeddedFallback)
	}
	if len(snapshot.LoadWarnings) == 0 {
		t.Fatalf("LoadWarnings = %#v, want fallback warning", snapshot.LoadWarnings)
	}
	if !strings.Contains(snapshot.LoadWarnings[0], "Falling back to embedded snapshot") {
		t.Fatalf("LoadWarnings[0] = %q, want fallback message", snapshot.LoadWarnings[0])
	}
}
