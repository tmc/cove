package covecli

import (
	"bytes"
	"strings"
	"testing"
)

func TestInventory(t *testing.T) {
	registry := []Spec{
		{Name: "commands", Summary: "Print inventory", Dispatch: DispatchEarly},
		{Name: "list", Aliases: []string{"ls"}, Summary: "List VMs", Dispatch: DispatchLate},
		{Name: "run", Summary: "Run VM", Dispatch: DispatchLate},
	}
	got := Inventory(registry)
	if len(got) != len(registry) {
		t.Fatalf("Inventory length = %d, want %d", len(got), len(registry))
	}
	if got[0].Name != "commands" || got[0].Dispatch != "early" || !got[0].SafeForDiscovery {
		t.Fatalf("commands inventory = %+v", got[0])
	}
	if got[1].Name != "list" || len(got[1].Aliases) != 1 || got[1].Aliases[0] != "ls" {
		t.Fatalf("list inventory = %+v", got[1])
	}
	if got[2].Name != "run" || !got[2].MutatesState || !got[2].MayBootVM {
		t.Fatalf("run inventory = %+v", got[2])
	}
}

func TestPrintCommandsTable(t *testing.T) {
	inventory := []Info{
		{Name: "commands", Summary: "Print inventory", Dispatch: "early", Outputs: []string{"text", "json"}},
		{Name: "list", Aliases: []string{"ls"}, Summary: "List VMs", Dispatch: "late", Outputs: []string{"text"}},
	}
	var out bytes.Buffer
	if err := PrintCommandsTable(&out, inventory); err != nil {
		t.Fatalf("PrintCommandsTable: %v", err)
	}
	text := out.String()
	for _, want := range []string{"COMMAND", "commands", "text,json", "list", "ls"} {
		if !strings.Contains(text, want) {
			t.Fatalf("table missing %q:\n%s", want, text)
		}
	}
}
