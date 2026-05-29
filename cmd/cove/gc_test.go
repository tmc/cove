package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOlderThanString(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "zero", in: 0, want: "any"},
		{name: "negative", in: -time.Second, want: "any"},
		{name: "positive minute", in: time.Minute, want: "1m0s"},
		{name: "positive hour", in: 2 * time.Hour, want: "2h0m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := olderThanString(tt.in)
			if got != tt.want {
				t.Fatalf("olderThanString(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHandleGCCommandFlagParseError(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown flag", args: []string{"-bogus"}},
		{name: "bad duration", args: []string{"-older-than", "nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handleGCCommand(tt.args)
			if err == nil {
				t.Fatalf("handleGCCommand(%v) = nil, want error", tt.args)
			}
		})
	}
}

func TestHandleGCCommandDryRunEmpty(t *testing.T) {
	tmp := withTempHome(t)
	if err := os.MkdirAll(filepath.Join(tmp, ".vz", "vms"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() error {
		return handleGCCommand([]string{"-dry-run"})
	})
	if !strings.Contains(out, "would be removed") {
		t.Fatalf("dry-run output = %q, want 'would be removed' message", out)
	}
	if !strings.Contains(out, "disposable: scanned=") {
		t.Fatalf("dry-run output = %q, want disposable summary line", out)
	}
	if !strings.Contains(out, "older-than=any") {
		t.Fatalf("dry-run output = %q, want older-than=any", out)
	}
	if !strings.Contains(out, "ephemeral:  scanned=") {
		t.Fatalf("dry-run output = %q, want ephemeral summary line", out)
	}
}

func TestHandleGCCommandRunEmpty(t *testing.T) {
	tmp := withTempHome(t)
	if err := os.MkdirAll(filepath.Join(tmp, ".vz", "vms"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() error {
		return handleGCCommand([]string{"-older-than", "1h"})
	})
	if !strings.Contains(out, "matched.") {
		t.Fatalf("run output = %q, want 'matched.' message", out)
	}
	if !strings.Contains(out, "older-than=1h0m0s") {
		t.Fatalf("run output = %q, want older-than=1h0m0s", out)
	}
}
