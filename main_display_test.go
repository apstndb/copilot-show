package main

import (
	"strings"
	"testing"

	"github.com/apstndb/copilot-show/pkg/analyze"
)

func TestFormatCompactCountSummary(t *testing.T) {
	t.Parallel()

	if got := formatCompactCountSummary(nil, 3); got != "-" {
		t.Fatalf("formatCompactCountSummary(nil) = %q, want -", got)
	}
	got := formatCompactCountSummary(map[string]int{
		"Read":  5,
		"Shell": 3,
		"Grep":  2,
		"Write": 1,
	}, 3)
	if got != "Read×5, Shell×3, Grep×2 +1" {
		t.Fatalf("formatCompactCountSummary() = %q, want compact top-N summary", got)
	}
}

func TestFormatQuotaMetricName(t *testing.T) {
	t.Parallel()

	if got := formatQuotaMetricName("premium_interactions"); got != "Premium Interactions" {
		t.Fatalf("formatQuotaMetricName(premium_interactions) = %q", got)
	}
	if got := formatQuotaMetricName("ai_credits"); got != "AI Credits" {
		t.Fatalf("formatQuotaMetricName(ai_credits) = %q", got)
	}
	if got := formatQuotaMetricName("chat_completions"); got != "Chat Completions" {
		t.Fatalf("formatQuotaMetricName(chat_completions) = %q", got)
	}
}

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

func TestFormatTableCellText(t *testing.T) {
	t.Parallel()

	wrapLongText = false
	t.Cleanup(func() { wrapLongText = false })

	if got := formatTableCellText("line one\nline two", 80); got != "line one" {
		t.Fatalf("formatTableCellText(compact) = %q, want first line only", got)
	}

	wrapLongText = true
	if got := formatTableCellText("line one\nline two", 80); got != "line one\nline two" {
		t.Fatalf("formatTableCellText(wrap) = %q, want full text", got)
	}
	if got := formatTableCellText("  ", 80); got != "-" {
		t.Fatalf("formatTableCellText(wrap, blank) = %q, want -", got)
	}
}

func TestFormatInlineScalarForTable(t *testing.T) {
	t.Parallel()

	wrapLongText = false
	t.Cleanup(func() { wrapLongText = false })

	long := strings.Repeat("x", 150)
	if got := formatInlineScalarForTable(long); len([]rune(got)) != 123 {
		t.Fatalf("formatInlineScalarForTable(compact) = %d runes, want 120 + ellipsis", len([]rune(got)))
	}

	wrapLongText = true
	if got := formatInlineScalarForTable(long); got != long {
		t.Fatalf("formatInlineScalarForTable(wrap) = %q, want full text", got)
	}
}

func TestFormatSessionTableCell(t *testing.T) {
	t.Parallel()

	wrapLongText = false
	t.Cleanup(func() { wrapLongText = false })

	long := strings.Repeat("a", 20)
	if got := formatSessionTableCell(long, 10); got != strings.Repeat("a", 10)+"\n"+strings.Repeat("a", 10) {
		t.Fatalf("formatSessionTableCell(compact) = %q, want manual wrap", got)
	}

	wrapLongText = true
	if got := formatSessionTableCell(long, 10); got != long {
		t.Fatalf("formatSessionTableCell(wrap) = %q, want full text", got)
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
