package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/covecli"
)

func TestCommandsJSONIncludesInventory(t *testing.T) {
	var stdout bytes.Buffer
	code := runCommandsCommand(commandEnv{Stdout: &stdout, Stderr: &bytes.Buffer{}}, "commands", []string{"--json"})
	if code != 0 {
		t.Fatalf("commands --json exit = %d", code)
	}
	var got []covecli.Info
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("commands JSON: %v\n%s", err, stdout.String())
	}
	have := map[string]covecli.Info{}
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
	if !have["commands"].SafeForDiscovery || have["commands"].MutatesState || have["commands"].RequiresRunningVM || have["commands"].MayBootVM {
		t.Fatalf("commands metadata = %+v, want safe read-only discovery", have["commands"])
	}
	if !have["runs"].SafeForDiscovery || have["runs"].MutatesState || have["runs"].RequiresRunningVM || have["runs"].MayBootVM {
		t.Fatalf("runs metadata = %+v, want safe read-only discovery", have["runs"])
	}
	if have["run"].SafeForDiscovery || !have["run"].MutatesState || !have["run"].MayBootVM {
		t.Fatalf("run metadata = %+v, want mutating boot command", have["run"])
	}
	if have["ctl"].SafeForDiscovery || !have["ctl"].RequiresRunningVM || have["ctl"].MutatesState || have["ctl"].MayBootVM {
		t.Fatalf("ctl metadata = %+v, want running-VM control command", have["ctl"])
	}
	if have["shared-folder"].SafeForDiscovery || !have["shared-folder"].MutatesState || have["shared-folder"].RequiresRunningVM || have["shared-folder"].MayBootVM {
		t.Fatalf("shared-folder metadata = %+v, want stateful non-boot command", have["shared-folder"])
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
