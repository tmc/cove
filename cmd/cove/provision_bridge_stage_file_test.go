package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageFileMkdirError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	manifest := &ProvisionManifest{}
	err := stageFile(blocker, "child/test.sh", []byte("data"), 0644, "", manifest)
	if err == nil || !strings.Contains(err.Error(), "create staging directory") {
		t.Fatalf("err = %v, want create staging directory", err)
	}
}
