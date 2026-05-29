package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteBuildCacheJSONMarshalError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	err := writeBuildCacheJSON(path, make(chan int))
	if err == nil {
		t.Fatal("err = nil, want json marshal error")
	}
}

func TestWriteBuildCacheJSONMkdirError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	path := filepath.Join(blocker, "cache.json")
	err := writeBuildCacheJSON(path, map[string]string{"a": "b"})
	if err == nil || !strings.Contains(err.Error(), "create build cache dir") {
		t.Fatalf("err = %v, want create build cache dir", err)
	}
}
