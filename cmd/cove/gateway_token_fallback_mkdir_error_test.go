package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayFallbackPathMkdirError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("HOME", blocker)
	_, err := gatewayFallbackPath()
	if err == nil || !strings.Contains(err.Error(), "create ~/.vz") {
		t.Fatalf("err = %v, want create ~/.vz", err)
	}
}
