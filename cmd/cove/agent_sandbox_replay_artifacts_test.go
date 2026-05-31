package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReplayArtifactsMkdirError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	replay := filepath.Join(blocker, "replay")
	_, err := writeReplayArtifacts(replay, filepath.Join(replay, "shots"), t.TempDir(), agentSandboxReplaySummary{FinalAnswer: "done"})
	if err == nil || !strings.Contains(err.Error(), "create replay dir") {
		t.Fatalf("err = %v, want create replay dir", err)
	}
}

func TestWriteReplayArtifactsEmptyAnswerFallback(t *testing.T) {
	root := t.TempDir()
	replay := filepath.Join(root, "replay")
	shots := filepath.Join(replay, "shots")
	src := t.TempDir()

	if _, err := writeReplayArtifacts(replay, shots, src, agentSandboxReplaySummary{FinalAnswer: "   "}); err != nil {
		t.Fatalf("writeReplayArtifacts: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(replay, "final-answer.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "no final answer") {
		t.Fatalf("final-answer.md = %q, want fallback", data)
	}
}

func TestWriteReplayArtifactsCountsControlEvents(t *testing.T) {
	root := t.TempDir()
	replay := filepath.Join(root, "replay")
	shots := filepath.Join(replay, "shots")
	src := t.TempDir()
	if err := os.MkdirAll(replay, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replay, "control-events.jsonl"), []byte("{}\n{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stats, err := writeReplayArtifacts(replay, shots, src, agentSandboxReplaySummary{
		RunID:       "run-2",
		VMName:      "vm-2",
		Provider:    "openai",
		Image:       "img:latest",
		Status:      "provider error",
		ReplayDir:   replay,
		MetricsPath: filepath.Join(root, "metrics.jsonl"),
		FinalAnswer: "partial answer",
	})
	if err != nil {
		t.Fatalf("writeReplayArtifacts: %v", err)
	}
	if stats.ControlEvents != 2 {
		t.Fatalf("ControlEvents = %d, want 2", stats.ControlEvents)
	}
	summary := readFile(t, filepath.Join(replay, "summary.md"))
	for _, want := range []string{"| Status | provider error |", "| Control events | 2 |", "partial answer"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}
