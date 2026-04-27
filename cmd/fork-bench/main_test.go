package main

import (
	"strings"
	"testing"
	"time"
)

func TestSplitCSV(t *testing.T) {
	got := splitCSV("a, b,,c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSanitizeName(t *testing.T) {
	if got, want := sanitizeName("macOS 15/base"), "macos-15-base"; got != want {
		t.Fatalf("sanitizeName = %q, want %q", got, want)
	}
}

func TestFormatMarkdown(t *testing.T) {
	doc := formatMarkdown(config{Runs: 1, Boot: true, MaxFork: 250 * time.Millisecond}, "darwin/arm64", "cove test", []result{
		{
			Parent:        "base",
			Child:         "child",
			ForkDuration:  140 * time.Millisecond,
			AgentDuration: 2 * time.Second,
			ParentDisk:    diskInfo{Logical: 60 * 1024 * 1024 * 1024, Allocated: 20 * 1024 * 1024, Inode: 1},
			ChildDisk:     diskInfo{Logical: 60 * 1024 * 1024 * 1024, Allocated: 1024 * 1024, Inode: 2},
		},
	})
	for _, want := range []string{
		"# cove fork benchmark",
		"| `base` | `child` | 60 GiB | 20 MiB | 1 MiB | true | 140ms | 2s | ok |",
		"Max fork threshold: 250ms",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("markdown missing %q:\n%s", want, doc)
		}
	}
}
