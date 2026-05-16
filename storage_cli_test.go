package main

import (
	"strings"
	"testing"
)

func TestStorageNoArgsShowsUsage(t *testing.T) {
	err := handleStorageCommand(nil)
	if err == nil || !strings.Contains(err.Error(), "command required") {
		t.Fatalf("err = %v, want command required", err)
	}
}

func TestStorageHelpUsage(t *testing.T) {
	var b strings.Builder
	printStorageUsage(&b)
	for _, want := range []string{"Usage: cove storage", "census", "budget", "prune"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("usage missing %q:\n%s", want, b.String())
		}
	}
}
