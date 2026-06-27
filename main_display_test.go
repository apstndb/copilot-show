package main

import (
	"testing"

	"github.com/apstndb/copilot-show/pkg/analyze"
)

func TestFirstLineTableText(t *testing.T) {
	t.Parallel()

	if got := firstLineTableText("", 80); got != "-" {
		t.Fatalf("firstLineTableText(empty) = %q, want -", got)
	}
	if got := firstLineTableText("line one\nline two", 80); got != "line one" {
		t.Fatalf("firstLineTableText() = %q, want first line only", got)
	}
	long := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
	if got := firstLineTableText(long, 10); got != "abcdefghij..." {
		t.Fatalf("firstLineTableText() = %q, want truncated", got)
	}
}

func TestFormatStatTokenCount(t *testing.T) {
	t.Parallel()

	if got := formatStatTokenCount(0); got != "0" {
		t.Fatalf("formatStatTokenCount(0) = %q, want 0", got)
	}
	if got := formatStatTokenCount(8303606); got != "8.3M" {
		t.Fatalf("formatStatTokenCount(8303606) = %q, want 8.3M", got)
	}
}

func TestSortStatsModelsPrefersHigherPremiumRequests(t *testing.T) {
	t.Parallel()

	stats := map[string]*analyze.ModelStat{
		"gpt-5.4":      {Cost: 10, Requests: 100},
		"claude-opus":  {Cost: 2, Requests: 20},
		"gpt-5.4-mini": {Cost: 10, Requests: 50},
	}
	models := []string{"claude-opus", "gpt-5.4-mini", "gpt-5.4"}
	sortStatsModels(models, stats)
	if models[0] != "gpt-5.4" || models[1] != "gpt-5.4-mini" || models[2] != "claude-opus" {
		t.Fatalf("sortStatsModels() = %#v", models)
	}
}

func TestSortRuntimeModelViewsPinsAutoLast(t *testing.T) {
	t.Parallel()

	models := []runtimeModelView{
		{ID: "auto", Name: "Auto"},
		{ID: "gpt-5.4", Name: "GPT-5.4"},
		{ID: "claude-haiku-4.5", Name: "Claude Haiku 4.5"},
	}
	sortRuntimeModelViews(models)
	if models[len(models)-1].ID != "auto" {
		t.Fatalf("sortRuntimeModelViews() = %#v, want auto last", models)
	}
}
