package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

func repoPath(tb testing.TB, elem ...string) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("resolve repo root")
	}
	parts := append([]string{filepath.Dir(file), "..", ".."}, elem...)
	return filepath.Clean(filepath.Join(parts...))
}
