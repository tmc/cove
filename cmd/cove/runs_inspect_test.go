package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunsInspectAndCompareRecordedFixture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".vz", "runs")
	for _, id := range []string{"left-task", "right-task"} {
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"manifest.json", "events.jsonl"} {
			data, err := os.ReadFile(filepath.Join("../../internal/runs/testdata/task-run", name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, format := range []string{"", "--json", "--html"} {
		var out bytes.Buffer
		args := []string{"left"}
		if format != "" {
			args = append(args, format)
		}
		if err := runRunsInspect(commandEnv{Stdout: &out}, args); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "no user session") {
			t.Fatalf("missing failure evidence: %s", out.String())
		}
	}
	var out bytes.Buffer
	if err := runRunsCompare(commandEnv{Stdout: &out}, []string{"left", "right", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"comparable": false`) || !strings.Contains(out.String(), "image identity unverified") {
		t.Fatalf("comparison=%s", out.String())
	}
}

func TestRunsInspectArgumentBounds(t *testing.T) {
	for _, args := range [][]string{nil, {"left", "right"}, {"left", "--json", "--html"}, {"--unexpected"}} {
		if err := runRunsInspect(commandEnv{Stdout: new(bytes.Buffer)}, args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
