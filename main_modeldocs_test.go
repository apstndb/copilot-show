package main

import (
	"testing"

	"github.com/apstndb/copilot-show/pkg/modeldocs"
)

func TestFormatSupportedPlansCompact(t *testing.T) {
	t.Parallel()

	if got := formatSupportedPlansCompact(nil); got != "-" {
		t.Fatalf("formatSupportedPlansCompact(nil) = %q, want -", got)
	}
	if got := formatSupportedPlansCompact([]string{"Pro", "Pro+", "Max"}); got != "Pro, Pro+, Max" {
		t.Fatalf("formatSupportedPlansCompact() = %q, want comma-separated plans", got)
	}
}

func TestFormatModelDocsModes(t *testing.T) {
	t.Parallel()

	if got := formatModelDocsModes(modeldocs.ModeAvailability{}); got != "-" {
		t.Fatalf("formatModelDocsModes(empty) = %q, want -", got)
	}
	if got := formatModelDocsModes(modeldocs.ModeAvailability{Agent: true, Edit: true}); got != "agent, edit" {
		t.Fatalf("formatModelDocsModes() = %q, want agent, edit", got)
	}
}

func TestFirstJoinedModelPolicyState(t *testing.T) {
	t.Parallel()

	if got := firstJoinedModelPolicyState(modeldocs.JoinedModel{}); got != "-" {
		t.Fatalf("firstJoinedModelPolicyState(empty) = %q, want -", got)
	}
	if got := firstJoinedModelPolicyState(modeldocs.JoinedModel{
		LiveModels: []modeldocs.LiveMatch{{PolicyState: "enabled"}},
	}); got != "enabled" {
		t.Fatalf("firstJoinedModelPolicyState() = %q, want enabled", got)
	}
}

func TestModelDocsTableRowAllLayout(t *testing.T) {
	t.Parallel()

	row := modelDocsTableRow(modeldocs.JoinedModel{
		Name:          "GPT-5.4",
		Provider:      "OpenAI",
		ReleaseStatus: "GA",
		Modes:         modeldocs.ModeAvailability{Agent: true, Ask: true, Edit: true},
		Clients:       modeldocs.ClientAvailability{CLI: true},
		VisibleNow:    true,
		Plans:         modeldocs.PlanAvailability{Pro: true, ProPlus: true},
		LiveModels: []modeldocs.LiveMatch{{
			TokenPricing: &modeldocs.SDKTokenPricing{
				InputUSDPerMTok:  float64Ptr(2.5),
				OutputUSDPerMTok: float64Ptr(15),
			},
			PolicyState: "enabled",
		}},
	}, true)
	if len(row) != 9 || row[3] != "2.5/15" || row[4] != "Yes" || row[6] != "enabled" || row[8] != "agent, ask, edit" {
		t.Fatalf("modelDocsTableRow(all) = %#v", row)
	}
}

func TestSortJoinedModelsForDisplay(t *testing.T) {
	t.Parallel()

	models := []modeldocs.JoinedModel{
		{Name: "Zeta", Provider: "OpenAI", VisibleNow: false},
		{Name: "Alpha", Provider: "Anthropic", VisibleNow: true, Clients: modeldocs.ClientAvailability{CLI: false}},
		{Name: "Beta", Provider: "Anthropic", VisibleNow: true, Clients: modeldocs.ClientAvailability{CLI: true}},
	}
	sortJoinedModelsForDisplay(models)
	if models[0].Name != "Beta" || models[1].Name != "Alpha" || models[2].Name != "Zeta" {
		t.Fatalf("sortJoinedModelsForDisplay() = %#v", models)
	}
}
