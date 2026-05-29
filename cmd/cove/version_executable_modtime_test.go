package main

import (
	"errors"
	"os"
	"testing"
)

func TestExecutableModTimeExecutableError(t *testing.T) {
	prev := versionExecutable
	t.Cleanup(func() { versionExecutable = prev })
	versionExecutable = func() (string, error) { return "", errors.New("nope") }
	if got := executableModTime(); got != "" {
		t.Fatalf("got = %q, want empty", got)
	}
}

func TestExecutableModTimeStatError(t *testing.T) {
	prevExe := versionExecutable
	prevStat := versionStat
	t.Cleanup(func() {
		versionExecutable = prevExe
		versionStat = prevStat
	})
	versionExecutable = func() (string, error) { return "/usr/bin/cove", nil }
	versionStat = func(string) (os.FileInfo, error) { return nil, errors.New("missing") }
	if got := executableModTime(); got != "" {
		t.Fatalf("got = %q, want empty", got)
	}
}
