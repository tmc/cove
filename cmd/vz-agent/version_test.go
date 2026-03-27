package main

import (
	"strings"
	"testing"
)

func TestAgentVersionRelease(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})

	version = "v1.2.3"
	commit = "abc12345"
	date = "2026-03-11T10:00:00Z"

	if got, want := agentVersion(), "v1.2.3"; got != want {
		t.Fatalf("agentVersion() = %q, want %q", got, want)
	}
	got := agentVersionInfo()
	wantPrefix := "vz-agent v1.2.3 (commit abc12345, built 2026-03-11T10:00:00Z, sha256:"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("agentVersionInfo() = %q, want prefix %q", got, wantPrefix)
	}
}

func TestAgentVersionDevFallsBackToCommit(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})

	version = "dev"
	commit = "deadbeef"
	date = "2026-03-11T10:00:00Z"

	if got, want := agentVersion(), "deadbeef"; got != want {
		t.Fatalf("agentVersion() = %q, want %q", got, want)
	}
	got := agentVersionInfo()
	wantPrefix := "vz-agent deadbeef (commit deadbeef, built 2026-03-11T10:00:00Z, sha256:"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("agentVersionInfo() = %q, want prefix %q", got, wantPrefix)
	}
}
