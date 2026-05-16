package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestVMTreeJSONMissingImageWritesError(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestVMTreeJSONExitHelper")
	cmd.Env = append(os.Environ(),
		"COVE_TEST_VM_TREE_JSON_EXIT=1",
		"HOME="+t.TempDir(),
	)
	out, err := cmd.Output()
	if err == nil {
		t.Fatal("vm tree helper succeeded; want exit error")
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("vm tree helper err = %v, want exit 1", err)
	}
	var got cliJSONError
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, string(out))
	}
	if got.OK || got.Command != "vm tree" || !strings.Contains(got.Error, "missing:latest") || got.Hint == "" {
		t.Fatalf("vm tree JSON error = %#v", got)
	}
}

func TestVMTreeJSONExitHelper(t *testing.T) {
	if os.Getenv("COVE_TEST_VM_TREE_JSON_EXIT") != "1" {
		return
	}
	handleVMCommand([]string{"tree", "--json", "--reachable-from", "missing:latest"})
	t.Fatal("handleVMCommand returned")
}
