package covecli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunCommandsCommandJSON(t *testing.T) {
	inventory := []Info{
		{Name: "commands", Summary: "Print inventory", Dispatch: "early", Outputs: []string{"text", "json"}},
		{Name: "list", Aliases: []string{"ls"}, Summary: "List VMs", Dispatch: "late", Outputs: []string{"text"}},
	}
	var stdout, stderr bytes.Buffer

	if code := RunCommandsCommand(&stdout, &stderr, []string{"--json"}, inventory); code != 0 {
		t.Fatalf("RunCommandsCommand exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	var got []Info
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("JSON output: %v\n%s", err, stdout.String())
	}
	if len(got) != 2 || got[1].Name != "list" || len(got[1].Aliases) != 1 || got[1].Aliases[0] != "ls" {
		t.Fatalf("inventory = %+v", got)
	}
}

func TestRunCommandsCommandTable(t *testing.T) {
	inventory := []Info{
		{Name: "commands", Summary: "Print inventory", Dispatch: "early", Outputs: []string{"text", "json"}},
	}
	var stdout, stderr bytes.Buffer

	if code := RunCommandsCommand(&stdout, &stderr, nil, inventory); code != 0 {
		t.Fatalf("RunCommandsCommand exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"COMMAND", "commands", "text,json"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("table missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunCommandsCommandUsageAndFlagError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunCommandsCommand(&stdout, &stderr, []string{"--help"}, nil); code != 0 {
		t.Fatalf("help exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage: cove commands") {
		t.Fatalf("help output = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunCommandsCommand(&stdout, &stderr, []string{"--bad"}, nil); code != 2 {
		t.Fatalf("bad flag exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage: cove commands") || !strings.Contains(stderr.String(), `error: unknown commands flag "--bad"`) {
		t.Fatalf("bad flag stderr = %q", stderr.String())
	}
}
