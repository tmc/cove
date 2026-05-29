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
	err := writeReplayArtifacts(replay, filepath.Join(replay, "shots"), t.TempDir(), "done")
	if err == nil || !strings.Contains(err.Error(), "create replay dir") {
		t.Fatalf("err = %v, want create replay dir", err)
	}
}

func TestWriteReplayArtifactsEmptyAnswerFallback(t *testing.T) {
	root := t.TempDir()
	replay := filepath.Join(root, "replay")
	shots := filepath.Join(replay, "shots")
	src := t.TempDir()

	if err := writeReplayArtifacts(replay, shots, src, "   "); err != nil {
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
