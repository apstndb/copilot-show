package modeldocs

import (
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
	snapshot := BuildSnapshot([]rpc.Model{
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
	})

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

	if len(snapshot.LiveModelsWithoutDocs) != 0 {
		t.Fatalf("LiveModelsWithoutDocs = %#v, want empty", snapshot.LiveModelsWithoutDocs)
	}
}
