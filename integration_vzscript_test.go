//go:build integration && darwin && arm64

package main

import (
	"fmt"
	"os"
	"testing"
)

func testVZScript(t *testing.T, vm *testVM) {
	t.Run("guest-shell", func(t *testing.T) {
		guestPath := "/tmp/vz-integration-vzscript-shell.txt"
		t.Cleanup(func() { cleanupGuestPaths(t, vm, guestPath) })

		scriptPath := writeTempVZScript(t, `# integration guest-shell smoke test
guest-shell write-marker.sh

-- write-marker.sh --
#!/bin/bash
set -eu
printf 'vzscript-ok\n' > /tmp/vz-integration-vzscript-shell.txt
`)
		runVZScriptFile(t, vm, scriptPath)

		if got := agentExec(t, vm, "/bin/cat", guestPath); got != "vzscript-ok\n" {
			t.Fatalf("vzscript guest-shell: got %q, want %q", got, "vzscript-ok\n")
		}
	})

	t.Run("append-path", func(t *testing.T) {
		const pathValue = "/tmp/vz-integration-append-path/bin"
		pathsDFile := "/etc/paths.d/" + pathsDName(pathValue)
		t.Cleanup(func() { cleanupGuestPaths(t, vm, pathsDFile) })

		scriptPath := writeTempVZScript(t, fmt.Sprintf(`# integration append-path smoke test
append-path %s
`, pathValue))
		runVZScriptFile(t, vm, scriptPath)

		if got := agentExec(t, vm, "/bin/cat", pathsDFile); got != pathValue+"\n" {
			t.Fatalf("append-path %q: got %q, want %q", pathsDFile, got, pathValue+"\n")
		}
	})
}

func writeTempVZScript(t *testing.T, content string) string {
	t.Helper()

	f, err := os.CreateTemp("", "vz-integration-*.vzscript")
	if err != nil {
		t.Fatalf("create temp vzscript: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		t.Fatalf("write temp vzscript %q: %v", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp vzscript %q: %v", f.Name(), err)
	}
	return f.Name()
}

func runVZScriptFile(t *testing.T, vm *testVM, scriptPath string) {
	t.Helper()

	if err := vzscriptRun([]string{"-socket", vm.sock, scriptPath}); err != nil {
		t.Fatalf("vzscript run %q: %v", scriptPath, err)
	}
}
