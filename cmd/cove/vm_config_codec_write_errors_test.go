package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFrameworkConfigBytesMkdirError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	target := filepath.Join(blocker, "child", "config.bin")
	err := writeFrameworkConfigBytes(target, []byte("data"))
	if err == nil || !strings.Contains(err.Error(), "create config directory") {
		t.Fatalf("err = %v, want create config directory", err)
	}
}

func TestWriteFrameworkConfigBytesWriteError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	err := writeFrameworkConfigBytes(target, []byte("data"))
	if err == nil || !strings.Contains(err.Error(), "write framework config") {
		t.Fatalf("err = %v, want write framework config", err)
	}
}
