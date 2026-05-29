package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestTraceEnableStartStopExport(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := makeTraceTestVM(t, "mac")
	env := commandTestEnv()
	oldNow := traceNow
	traceNow = func() time.Time { return time.Date(2026, 5, 13, 23, 45, 0, 0, time.UTC) }
	t.Cleanup(func() { traceNow = oldNow })

	if err := runTraceEnable(env, []string{"mac"}); err != nil {
		t.Fatalf("runTraceEnable: %v", err)
	}
	var cfg esloggerTraceConfig
	readJSON(t, traceConfigPath(dir), &cfg)
	if !cfg.Enabled || cfg.VMName != "mac" {
		t.Fatalf("config = %+v", cfg)
	}

	if err := runTraceStart(env, []string{"--id", "trace-a", "mac"}); err != nil {
		t.Fatalf("runTraceStart: %v", err)
	}
	var session esloggerTraceSession
	readJSON(t, traceSessionPath(dir, "trace-a"), &session)
	if session.Status != "unsupported" || !strings.Contains(session.Note, "eslogger") {
		t.Fatalf("session = %+v", session)
	}
	if err := os.WriteFile(session.LogPath, []byte(`{"event":"login"}`+"\n"), 0644); err != nil {
		t.Fatalf("write trace log: %v", err)
	}

	if err := runTraceStop(env, []string{"mac", "--id", "trace-a"}); err != nil {
		t.Fatalf("runTraceStop: %v", err)
	}
	readJSON(t, traceSessionPath(dir, "trace-a"), &session)
	if session.Status != "stopped" || session.StoppedAt == "" {
		t.Fatalf("stopped session = %+v", session)
	}

	out := filepath.Join(t.TempDir(), "trace.tar.gz")
	if err := runTraceExport(env, []string{"mac", "--id", "trace-a", "--out", out}); err != nil {
		t.Fatalf("runTraceExport: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	names := tarNames(t, data)
	for _, want := range []string{"trace-a/session.json", "trace-a/eslogger.jsonl"} {
		if !names[want] {
			t.Fatalf("trace export missing %q: %#v", want, names)
		}
	}
}

func TestTraceRejectsUnsupportedGuest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := vmconfig.BaseDir()
	if err := os.MkdirAll(filepath.Join(base, "linux"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "linux", "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	err := runTraceEnable(commandTestEnv(), []string{"linux"})
	if err == nil || !strings.Contains(err.Error(), "supported for macOS") {
		t.Fatalf("runTraceEnable linux err = %v", err)
	}
}

func TestTraceStatusNoSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	makeTraceTestVM(t, "mac")
	env := commandTestEnv()
	if err := runTraceStatus(env, []string{"mac"}); err != nil {
		t.Fatalf("runTraceStatus: %v", err)
	}
	out := env.Stdout.(*bytes.Buffer)
	if !strings.Contains(out.String(), "enabled: no") || !strings.Contains(out.String(), "latest: none") {
		t.Fatalf("status output = %q", out.String())
	}
}

func TestTraceStatusJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := makeTraceTestVM(t, "mac")
	if err := writeJSONFile(traceConfigPath(dir), esloggerTraceConfig{
		VMName:    "mac",
		Enabled:   true,
		UpdatedAt: "2026-05-14T00:00:00Z",
	}); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := writeJSONFile(traceSessionPath(dir, "trace-a"), esloggerTraceSession{
		ID:      "trace-a",
		VMName:  "mac",
		Status:  "unsupported",
		LogPath: filepath.Join(traceSessionDir(dir, "trace-a"), "eslogger.jsonl"),
	}); err != nil {
		t.Fatalf("write session: %v", err)
	}
	env := commandTestEnv()
	if err := runTraceStatus(env, []string{"mac", "--json"}); err != nil {
		t.Fatalf("runTraceStatus --json: %v", err)
	}
	var got traceStatusOutput
	out := env.Stdout.(*bytes.Buffer).String()
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("status JSON: %v\n%s", err, out)
	}
	if got.VMName != "mac" || !got.Enabled || got.Latest == nil || got.Latest.ID != "trace-a" {
		t.Fatalf("status JSON = %+v", got)
	}
	if got.Capabilities.GuestCaptureWired {
		t.Fatalf("capabilities = %+v, want guest_capture_wired false", got.Capabilities)
	}
}

func TestTraceStatusJSONMissingVMWritesError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := commandTestEnv()
	if err := runTraceStatus(env, []string{"missing-trace-vm", "--json"}); err == nil {
		t.Fatal("runTraceStatus missing VM succeeded")
	}
	var got cliJSONError
	out := env.Stdout.(*bytes.Buffer).String()
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("trace status error output is not JSON: %v\n%s", jsonErr, out)
	}
	if got.OK || got.Command != "trace status" || !strings.Contains(got.Error, `no VM named "missing-trace-vm"`) || got.Hint == "" {
		t.Fatalf("trace status JSON error = %#v", got)
	}
}

func TestTraceCapabilitiesJSON(t *testing.T) {
	env := commandTestEnv()
	if err := runTraceCapabilities(env, []string{"--json"}); err != nil {
		t.Fatalf("runTraceCapabilities --json: %v", err)
	}
	var got traceCapabilities
	out := env.Stdout.(*bytes.Buffer).String()
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("capabilities JSON: %v\n%s", err, out)
	}
	if len(got.SupportedGuests) != 1 || got.SupportedGuests[0] != "macOS" || got.GuestCaptureWired {
		t.Fatalf("capabilities = %+v", got)
	}
}

func TestHandleTraceCommandUsesEnvWriters(t *testing.T) {
	env := commandTestEnv()
	if err := handleTraceCommand(env, []string{"capabilities"}); err != nil {
		t.Fatalf("handleTraceCommand capabilities: %v", err)
	}
	out := env.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "supported guests: macOS") {
		t.Fatalf("stdout = %q", out)
	}
	if got := env.Stderr.(*bytes.Buffer).String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func makeTraceTestVM(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(vmconfig.BaseDir(), name)
	for _, file := range []string{"disk.img", "aux.img", "hw.model"} {
		path := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(file), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}
