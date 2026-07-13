package main

import (
	"slices"
	"strings"
	"testing"
)

func TestParseSessionEventTypeNames(t *testing.T) {
	t.Parallel()

	source := []byte(`package sample

const (
	SessionEventTypeUserMessage = "user.message"
	UnrelatedConstant = "unrelated"
	SessionEventTypeAssistantIdle = "assistant.idle"
)
`)
	want := []string{
		"SessionEventTypeAssistantIdle",
		"SessionEventTypeUserMessage",
	}

	got, err := parseSessionEventTypeNames("sample.go", source)
	if err != nil {
		t.Fatalf("parseSessionEventTypeNames() error = %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("parseSessionEventTypeNames() = %q, want %q", got, want)
	}
}

func TestRenderSessionEventTypesIncludesModuleVersion(t *testing.T) {
	t.Parallel()

	generated, err := renderSessionEventTypes(
		"v1.2.3",
		[]string{"SessionEventTypeUserMessage"},
	)
	if err != nil {
		t.Fatalf("renderSessionEventTypes() error = %v", err)
	}
	text := string(generated)
	for _, want := range []string{
		"// Source: github.com/github/copilot-sdk/go v1.2.3.",
		"copilot.SessionEventTypeUserMessage: {}",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("renderSessionEventTypes() output does not contain %q:\n%s", want, text)
		}
	}
}
