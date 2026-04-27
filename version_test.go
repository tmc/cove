package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolvedVersionFallsBackToExecutableGitAndModTime(t *testing.T) {
	save := saveVersionState()
	defer save()

	dir := t.TempDir()
	exe := filepath.Join(dir, "cove")
	if err := os.WriteFile(exe, []byte("x"), 0700); err != nil {
		t.Fatal(err)
	}
	modTime := time.Date(2026, 4, 27, 12, 34, 56, 0, time.UTC)

	version = "dev"
	commit = "unknown"
	date = "unknown"
	versionExecutable = func() (string, error) { return exe, nil }
	versionGetwd = func() (string, error) { return "/no/git/here", nil }
	versionStat = func(path string) (os.FileInfo, error) {
		if path != exe {
			t.Fatalf("stat path = %q, want %q", path, exe)
		}
		return fakeFileInfo{modTime: modTime}, nil
	}
	versionGitOutput = func(dir string, args ...string) ([]byte, error) {
		if dir != filepath.Dir(exe) {
			return nil, errors.New("not a git dir")
		}
		return []byte("abc123def456\n"), nil
	}

	got := resolvedVersion()
	if got.Commit != "abc123def456" {
		t.Fatalf("Commit = %q, want abc123def456", got.Commit)
	}
	if got.Date != "2026-04-27T12:34:56Z" {
		t.Fatalf("Date = %q, want 2026-04-27T12:34:56Z", got.Date)
	}
	if hostVersion() != "abc123def456" {
		t.Fatalf("hostVersion() = %q, want abc123def456", hostVersion())
	}
}

func TestResolvedVersionKeepsInjectedValues(t *testing.T) {
	save := saveVersionState()
	defer save()

	version = "v1.2.3"
	commit = "abc123"
	date = "2026-04-27T00:00:00Z"
	versionGitOutput = func(string, ...string) ([]byte, error) {
		t.Fatal("git fallback should not run")
		return nil, nil
	}

	got := resolvedVersion()
	if got.Version != version || got.Commit != commit || got.Date != date {
		t.Fatalf("resolvedVersion() = %#v", got)
	}
}

func saveVersionState() func() {
	oldVersion, oldCommit, oldDate := version, commit, date
	oldExecutable := versionExecutable
	oldStat := versionStat
	oldGetwd := versionGetwd
	oldGitOutput := versionGitOutput
	return func() {
		version, commit, date = oldVersion, oldCommit, oldDate
		versionExecutable = oldExecutable
		versionStat = oldStat
		versionGetwd = oldGetwd
		versionGitOutput = oldGitOutput
	}
}

type fakeFileInfo struct {
	modTime time.Time
}

func (f fakeFileInfo) Name() string       { return "cove" }
func (f fakeFileInfo) Size() int64        { return 1 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0700 }
func (f fakeFileInfo) ModTime() time.Time { return f.modTime }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }
