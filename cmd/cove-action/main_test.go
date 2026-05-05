package main

import (
	"bufio"
	"encoding/json"
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
		"metrics-path=" + filepath.Join(dir, ".vz", "runs", "stub-run", "metrics.jsonl"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("outputs missing %q in:\n%s", want, got)
		}
	}
	events := readEvents(t, filepath.Join(dir, ".vz", "runs", "stub-run", "metrics.jsonl"))
	for _, want := range []string{"action_start", "vm_create", "vm_start", "agent_ready", "command_complete", "action_complete"} {
		if !events[want] {
			t.Fatalf("missing event %q in %#v", want, events)
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
	vm=""
	image=""
	prev=""
	for arg in "$@"; do
		if [ "$prev" = "-fork-name" ]; then vm="$arg"; fi
		if [ "$prev" = "-fork-from" ]; then image="$arg"; fi
		prev="$arg"
	done
	metrics_dir="$HOME/.vz/runs/stub-run"
	mkdir -p "$metrics_dir"
	{
		printf '{"timestamp":"2026-05-05T00:00:00Z","event_type":"vm_create","vm_name":"%s","image_ref":"%s","status":"ok"}\n' "$vm" "$image"
		printf '{"timestamp":"2026-05-05T00:00:01Z","event_type":"fork_created","vm_name":"%s","image_ref":"%s","status":"ok"}\n' "$vm" "$image"
		printf '{"timestamp":"2026-05-05T00:00:02Z","event_type":"vm_start","vm_name":"%s","image_ref":"%s","status":"ok"}\n' "$vm" "$image"
		printf '{"timestamp":"2026-05-05T00:00:03Z","event_type":"agent_ready","vm_name":"%s","image_ref":"%s","status":"ok"}\n' "$vm" "$image"
	} > "$metrics_dir/metrics.jsonl"
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

func readEvents(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	events := map[string]bool{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var row struct {
			EventType string `json:"event_type"`
		}
		if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		events[row.EventType] = true
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return events
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
