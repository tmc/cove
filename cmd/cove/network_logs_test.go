package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseNetworkLogsArgs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantVM     string
		wantFollow bool
		wantErr    bool
	}{
		{name: "plain", args: []string{"vm1"}, wantVM: "vm1"},
		{name: "follow short", args: []string{"-f", "vm1"}, wantVM: "vm1", wantFollow: true},
		{name: "follow long", args: []string{"--follow", "vm1"}, wantVM: "vm1", wantFollow: true},
		{name: "missing", wantErr: true},
		{name: "extra", args: []string{"vm1", "x"}, wantErr: true},
		{name: "path", args: []string{"../vm1"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseNetworkLogsArgs(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatal("parseNetworkLogsArgs succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseNetworkLogsArgs: %v", err)
			}
			if got.VM != tc.wantVM || got.Follow != tc.wantFollow {
				t.Fatalf("parseNetworkLogsArgs = %#v, want vm=%q follow=%v", got, tc.wantVM, tc.wantFollow)
			}
		})
	}
}

func TestPrintNetworkLogsSelectsNewestVMRun(t *testing.T) {
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prevRuns })

	writeNetworkLogRun(t, runsRoot, "old", "vm1", "2026-05-05T10:00:00Z", "old\n")
	writeNetworkLogRun(t, runsRoot, "other", "vm2", "2026-05-05T12:00:00Z", "other\n")
	writeNetworkLogRun(t, runsRoot, "new", "vm1", "2026-05-05T11:00:00Z", "new\n")

	var out bytes.Buffer
	if err := PrintNetworkLogs(&out, "vm1", false); err != nil {
		t.Fatalf("PrintNetworkLogs: %v", err)
	}
	if out.String() != "new\n" {
		t.Fatalf("PrintNetworkLogs output = %q, want newest vm1 log", out.String())
	}
}

func TestPrintNetworkLogsMissingVM(t *testing.T) {
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prevRuns })

	writeNetworkLogRun(t, runsRoot, "run1", "vm1", "2026-05-05T10:00:00Z", "log\n")
	var out bytes.Buffer
	if err := PrintNetworkLogs(&out, "missing", false); err == nil {
		t.Fatal("PrintNetworkLogs succeeded, want missing VM error")
	}
}

func writeNetworkLogRun(t *testing.T, root, runID, vm, ts, log string) {
	t.Helper()
	dir := filepath.Join(root, runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	metrics := `{"timestamp":"` + ts + `","event_type":"vm_start","vm_name":"` + vm + `","status":"ok","extra":{"run_id":"` + runID + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "metrics.jsonl"), []byte(metrics), 0644); err != nil {
		t.Fatalf("WriteFile(metrics): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "network.log"), []byte(log), 0644); err != nil {
		t.Fatalf("WriteFile(network.log): %v", err)
	}
}
