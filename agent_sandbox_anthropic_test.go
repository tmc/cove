package main

import (
	"testing"

	"github.com/tmc/vz-macos/internal/anthropicadapter"
)

func TestFinalAnthropicAnswer(t *testing.T) {
	got := finalAnthropicAnswer([]anthropicadapter.TranscriptEntry{
		{Type: "tool_result", Text: "ignored"},
		{Type: "response", Text: "done"},
	})
	if got != "done" {
		t.Fatalf("finalAnthropicAnswer = %q, want done", got)
	}
}

func TestAnthropicModelDefault(t *testing.T) {
	t.Setenv("COVE_ANTHROPIC_MODEL", "")
	if got := anthropicModel(); got != "claude-opus-4-7" {
		t.Fatalf("anthropicModel = %q", got)
	}
}
