package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestDefaultTableMaxWidth(t *testing.T) {
	if got := calculateTableMaxWidth("markdown", 120); got != 0 {
		t.Fatalf("calculateTableMaxWidth(markdown, 120) = %d, want 0", got)
	}
	if got := calculateTableMaxWidth("default", 0); got != 0 {
		t.Fatalf("calculateTableMaxWidth(default, 0) = %d, want 0", got)
	}
	if got := calculateTableMaxWidth("default", 80); got != 78 {
		t.Fatalf("calculateTableMaxWidth(default, 80) = %d, want 78", got)
	}
	if got := calculateTableMaxWidth("default", 60); got != 58 {
		t.Fatalf("calculateTableMaxWidth(default, 60) = %d, want 58", got)
	}
}

func TestNewTableWrapsRowsWhenMaxWidthIsConstrained(t *testing.T) {
	t.Parallel()

	renderTable := func(maxWidth int) string {
		var buf bytes.Buffer
		table := newTable(&buf, []string{"Value"}, nil, false, false, "default", maxWidth)
		table.Append([]string{"alpha beta gamma delta epsilon zeta eta theta"})
		table.Render()
		return buf.String()
	}

	unwrapped := renderTable(0)
	wrapped := renderTable(20)
	if strings.Count(wrapped, "\n") <= strings.Count(unwrapped, "\n") {
		t.Fatalf("wrapped output did not gain lines:\n--- unwrapped ---\n%s\n--- wrapped ---\n%s", unwrapped, wrapped)
	}
}

func TestNewTableBreaksUnspacedTokensWhenMaxWidthIsConstrained(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	table := newTable(&buf, []string{"Identifier"}, nil, false, false, "default", 20)
	table.Append([]string{"claude-sonnet-4.6"})
	table.Render()

	got := buf.String()
	if !strings.Contains(got, "claude-son") || !strings.Contains(got, "net-4.6") {
		t.Fatalf("newTable() did not break unspaced token as expected:\n%s", got)
	}
}

func TestShouldUseCompactPadding(t *testing.T) {
	if shouldUseCompactPadding("markdown", 12, 41) {
		t.Fatal("shouldUseCompactPadding(markdown, 12, 41) = true, want false")
	}
	if !shouldUseCompactPadding("default", 12, 60) {
		t.Fatal("shouldUseCompactPadding(default, 12, 60) = false, want true")
	}
	if shouldUseCompactPadding("default", 10, 51) {
		t.Fatal("shouldUseCompactPadding(default, 10, 51) = true, want false")
	}
}

func TestTableWidthFromEnv(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	t.Setenv("COLUMN", "90")
	if got, ok := tableWidthFromEnv(); !ok || got != 120 {
		t.Fatalf("tableWidthFromEnv() = %d, %v, want 120, true", got, ok)
	}

	t.Setenv("COLUMNS", "invalid")
	if got, ok := tableWidthFromEnv(); !ok || got != 90 {
		t.Fatalf("tableWidthFromEnv() fallback = %d, %v, want 90, true", got, ok)
	}

	t.Setenv("COLUMNS", "0")
	if got, ok := tableWidthFromEnv(); !ok || got != 0 {
		t.Fatalf("tableWidthFromEnv() zero disable = %d, %v, want 0, true", got, ok)
	}

	t.Setenv("COLUMNS", "-1")
	if got, ok := tableWidthFromEnv(); !ok || got != -1 {
		t.Fatalf("tableWidthFromEnv() negative disable = %d, %v, want -1, true", got, ok)
	}

	t.Setenv("COLUMNS", "")
	t.Setenv("COLUMN", "")
	if got, ok := tableWidthFromEnv(); ok || got != 0 {
		t.Fatalf("tableWidthFromEnv() empty = %d, %v, want 0, false", got, ok)
	}
}

func TestDefaultTableMaxWidthDisableOverride(t *testing.T) {
	SetTableWidthOverride(-1)
	t.Cleanup(func() { SetTableWidthOverride(0) })

	if got := defaultTableMaxWidth("default"); got != 0 {
		t.Fatalf("defaultTableMaxWidth(default) with disable override = %d, want 0", got)
	}
}

func TestDefaultTableMaxWidthFoldSkipsTerminalAutoWidth(t *testing.T) {
	SetTableFoldEnabled(true)
	t.Cleanup(func() { SetTableFoldEnabled(true) })
	SetTableWidthOverride(0)
	t.Cleanup(func() { SetTableWidthOverride(0) })

	if got := defaultTableMaxWidth("default"); got != 0 {
		t.Fatalf("defaultTableMaxWidth(default) with fold enabled = %d, want 0", got)
	}
}

func TestDefaultTableMaxWidthLegacyUsesTerminalAutoWidth(t *testing.T) {
	SetTableFoldEnabled(false)
	t.Cleanup(func() { SetTableFoldEnabled(true) })
	SetTableWidthOverride(0)
	t.Cleanup(func() { SetTableWidthOverride(0) })

	if got := defaultTableMaxWidth("default"); got != 0 {
		t.Fatalf("defaultTableMaxWidth(default) with legacy layout = %d, want non-zero terminal width when available", got)
	}
}

func TestNewTableFoldPreservesUnspacedTokens(t *testing.T) {
	t.Parallel()

	SetTableFoldEnabled(true)
	t.Cleanup(func() { SetTableFoldEnabled(true) })

	var buf bytes.Buffer
	table := newTable(&buf, []string{"Identifier"}, nil, false, false, "default", 20)
	table.Append([]string{"claude-sonnet-4.6"})
	table.Render()

	got := buf.String()
	if !strings.Contains(got, "claude-sonnet-4.6") {
		t.Fatalf("fold layout should preserve identifier:\n%s", got)
	}
	if strings.Contains(got, "claude-son\n") || strings.Contains(got, "son\nnet") {
		t.Fatalf("fold layout should not break unspaced tokens across lines:\n%s", got)
	}
}
