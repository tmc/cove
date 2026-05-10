package main

import "testing"

func TestDetectRuntimeStateEmptyDir(t *testing.T) {
	if got := detectRuntimeState(t.TempDir()); got != "" {
		t.Fatalf("got = %q, want empty (no runtime state)", got)
	}
}

func TestDetectRuntimeStateMissingDir(t *testing.T) {
	if got := detectRuntimeState("/nonexistent/cove-vm-r347"); got != "" {
		t.Fatalf("got = %q, want empty (missing dir)", got)
	}
}
