package modeldocs

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/github/copilot-sdk/go/rpc"
)

func TestNormalizeModelNameKey(t *testing.T) {
	tests := []struct {
		a string
		b string
	}{
		{"Gemini 3 Pro (Preview)", "gemini-3-pro-preview"},
		{"Claude Sonnet 4.0", "claude-sonnet-4"},
		{"GPT-5.1 Codex Max", "gpt-5.1-codex-max"},
	}

	for _, tt := range tests {
		if gotA, gotB := NormalizeModelNameKey(tt.a), NormalizeModelNameKey(tt.b); gotA != gotB {
			t.Fatalf("NormalizeModelNameKey(%q) = %q, NormalizeModelNameKey(%q) = %q", tt.a, gotA, tt.b, gotB)
		}
	}
}

func TestBuildSnapshotMatchesPreviewNames(t *testing.T) {
	snapshot, err := BuildSnapshotWithOptions(context.Background(), []rpc.Model{
		{
			ID:   "gemini-3-pro-preview",
			Name: "Gemini 3 Pro (Preview)",
			Policy: &rpc.Policy{
				State: "enabled",
			},
			Billing: &rpc.Billing{
				Multiplier: 1,
			},
		},
		{
			ID:   "claude-sonnet-4",
			Name: "Claude Sonnet 4",
			Policy: &rpc.Policy{
				State: "enabled",
			},
			Billing: &rpc.Billing{
				Multiplier: 1,
			},
		},
	}, SnapshotOptions{})
	if err != nil {
		t.Fatalf("BuildSnapshotWithOptions() error = %v", err)
	}

	var geminiPro JoinedModel
	var gemini31 JoinedModel
	foundGeminiPro := false
	foundGemini31 := false
	for _, model := range snapshot.Models {
		switch model.Name {
		case "Gemini 3 Pro":
			geminiPro = model
			foundGeminiPro = true
		case "Gemini 3.1 Pro":
			gemini31 = model
			foundGemini31 = true
		}
	}

	if !foundGeminiPro {
		t.Fatalf("Gemini 3 Pro docs row not found")
	}
	if !geminiPro.VisibleNow {
		t.Fatalf("Gemini 3 Pro should be matched as visible")
	}
	if len(geminiPro.LiveModels) != 1 || geminiPro.LiveModels[0].ID != "gemini-3-pro-preview" {
		t.Fatalf("Gemini 3 Pro live matches = %#v, want gemini-3-pro-preview", geminiPro.LiveModels)
	}

	if !foundGemini31 {
		t.Fatalf("Gemini 3.1 Pro docs row not found")
	}
	if gemini31.VisibleNow {
		t.Fatalf("Gemini 3.1 Pro should remain not visible in this fixture")
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
			return []byte("- name: Gemini 3 Pro\n  free: false\n  student: true\n  pro: true\n  pro_plus: true\n  business: true\n  enterprise: true\n"), nil
		case strings.HasSuffix(url, "/model-comparison.yml"):
			return []byte("[]\n"), nil
		case strings.HasSuffix(url, "/model-deprecation-history.yml"):
			return []byte("[]\n"), nil
		default:
			return nil, fmt.Errorf("unexpected url %s", url)
		}
	}

	snapshot, err := buildSnapshotWithFetcher(context.Background(), []rpc.Model{{ID: "gemini-3-pro-preview", Name: "Gemini 3 Pro (Preview)"}}, SnapshotOptions{PreferLatest: true}, fetcher)
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
