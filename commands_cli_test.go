package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCommandsJSONIncludesInventory(t *testing.T) {
	var stdout bytes.Buffer
	code := runCommandsCommand(commandEnv{Stdout: &stdout, Stderr: &bytes.Buffer{}}, "commands", []string{"--json"})
	if code != 0 {
		t.Fatalf("commands --json exit = %d", code)
	}
	var got []commandInfo
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("commands JSON: %v\n%s", err, stdout.String())
	}
	have := map[string]commandInfo{}
	for _, info := range got {
		have[info.Name] = info
	}
	for _, name := range []string{"commands", "recording", "runs", "trace"} {
		if have[name].Name == "" {
			t.Fatalf("commands JSON missing %q", name)
		}
	}
	if !commandsContainsString(have["recording"].Outputs, "json") {
		t.Fatalf("recording outputs = %v, want json", have["recording"].Outputs)
	}
	if !commandsContainsString(have["list"].Aliases, "ls") {
		t.Fatalf("list aliases = %v, want ls", have["list"].Aliases)
	}
	if have["commands"].Dispatch != "early" {
		t.Fatalf("commands dispatch = %q, want early", have["commands"].Dispatch)
	}
}

func TestHelpJSON(t *testing.T) {
	out, err := captureStdoutResult(t, func() error {
		handled, code := handleEarlyCLI([]string{"help", "--json"})
		if !handled || code != 0 {
			t.Fatalf("handleEarlyCLI(help --json) = %v, %d; want true, 0", handled, code)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("help --json: %v", err)
	}
	if !strings.Contains(out, `"name": "commands"`) {
		t.Fatalf("help --json missing commands:\n%s", out)
	}
}

func commandsContainsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
