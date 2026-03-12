package main

import "testing"

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
	if got, want := agentVersionInfo(), "vz-agent v1.2.3 (commit abc12345, built 2026-03-11T10:00:00Z)"; got != want {
		t.Fatalf("agentVersionInfo() = %q, want %q", got, want)
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
	if got, want := agentVersionInfo(), "vz-agent deadbeef (commit deadbeef, built 2026-03-11T10:00:00Z)"; got != want {
		t.Fatalf("agentVersionInfo() = %q, want %q", got, want)
	}
}
