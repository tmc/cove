package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunSuccess(t *testing.T) {
	dir := t.TempDir()
	oldCleanupWait := cleanupWait
	cleanupWait = 10 * time.Millisecond
	t.Cleanup(func() { cleanupWait = oldCleanupWait })
	stub := writeStubCove(t, dir, 0)
	out := filepath.Join(dir, "out")
	code := run([]string{"-cove-bin", stub, "-image", "ubuntu-ci", "-command", "echo ok", "-env", "TOKEN=secret\nEMPTY="}, []string{
		"HOME=" + dir,
		"GITHUB_OUTPUT=" + out,
		"GITHUB_RUN_ID=123",
		"GITHUB_RUN_ATTEMPT=2",
		"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
		"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
	}, os.Stdout, os.Stderr)
	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	got := readFile(t, out)
	for _, want := range []string{
		"vm-name=cove-action-123-2",
		"exit-code=0",
		"log-path=" + filepath.Join(dir, ".vz", "runs"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("outputs missing %q in:\n%s", want, got)
		}
	}
	log := readFile(t, filepath.Join(dir, "log"))
	for _, want := range []string{
		"run -fork-from ubuntu-ci -fork-name cove-action-123-2 -ephemeral -headless",
		"shell cove-action-123-2 -- /bin/sh -lc true",
		"env 'TOKEN=secret' 'EMPTY=' /bin/sh -lc 'echo ok'",
		"ctl -vm cove-action-123-2 stop",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q in:\n%s", want, log)
		}
	}
}

func TestRunReturnsGuestExitCode(t *testing.T) {
	dir := t.TempDir()
	oldCleanupWait := cleanupWait
	cleanupWait = 10 * time.Millisecond
	t.Cleanup(func() { cleanupWait = oldCleanupWait })
	stub := writeStubCove(t, dir, 7)
	code := run([]string{"-cove-bin", stub, "-image", "ubuntu-ci", "-command", "exit 7"}, []string{
		"HOME=" + dir,
		"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
		"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
	}, os.Stdout, os.Stderr)
	if code != 7 {
		t.Fatalf("run returned %d, want 7", code)
	}
	log := readFile(t, filepath.Join(dir, "log"))
	if !strings.Contains(log, "ctl -vm cove-action-local-1 stop") {
		t.Fatalf("cleanup did not stop VM:\n%s", log)
	}
}

func TestParseConfigRequiresCommand(t *testing.T) {
	_, err := parseConfig(nil, []string{"COVE_ACTION_IMAGE=ubuntu-ci"}, os.Stdout, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "command or script is required") {
		t.Fatalf("parseConfig error = %v, want missing command", err)
	}
}

func writeStubCove(t *testing.T, dir string, jobExit int) string {
	t.Helper()
	path := filepath.Join(dir, "cove-stub")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$COVE_STUB_LOG"
case "$1" in
run)
	trap 'exit 0' TERM INT
	while :; do sleep 1; done
	;;
shell)
	count=0
	if [ -f "$COVE_STUB_COUNT" ]; then
		count=$(cat "$COVE_STUB_COUNT")
	fi
	next=$((count + 1))
	printf '%s' "$next" > "$COVE_STUB_COUNT"
	if [ "$count" = 0 ]; then
		exit 0
	fi
	exit ` + strconv.Itoa(jobExit) + `
	;;
ctl)
	exit 0
	;;
*)
	exit 2
	;;
esac
`
	if runtime.GOOS == "windows" {
		t.Skip("shell stub requires sh")
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
