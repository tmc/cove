package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageAutoLoginStageError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	manifest := &ProvisionManifest{}
	err := stageAutoLogin(blocker, "user", "pw", manifest)
	if err == nil {
		t.Fatal("err = nil, want stageFile error")
	}
}
