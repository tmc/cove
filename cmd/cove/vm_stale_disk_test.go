package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestStaleDiskLeaseFor(t *testing.T) {
	holders := []vmDiskHolder{{PID: 53574, Command: "com.apple.Virtualization.VirtualMachine.xpc"}}
	tests := []struct {
		name    string
		running bool
		holders []vmDiskHolder
		want    bool
	}{
		{"stopped with holder", false, holders, true},
		{"running with holder", true, holders, false},
		{"stopped no holder", false, nil, false},
		{"running no holder", true, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lease := staleDiskLeaseFor("default", "/vm/default.covevm", tt.running, tt.holders)
			if (lease != nil) != tt.want {
				t.Fatalf("staleDiskLeaseFor lease=%v want present=%v", lease, tt.want)
			}
		})
	}
}

func TestStaleDiskLeaseSummary(t *testing.T) {
	tests := []struct {
		name    string
		holders []vmDiskHolder
		want    string
	}{
		{
			"single",
			[]vmDiskHolder{{PID: 53574}},
			"default: disk held by stale VZ process (pid 53574) though VM is stopped",
		},
		{
			"multiple",
			[]vmDiskHolder{{PID: 10}, {PID: 20}},
			"default: disk held by stale VZ process (pids 10, 20) though VM is stopped",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lease := staleDiskLease{Name: "default", Holders: tt.holders}
			if got := lease.summary(); got != tt.want {
				t.Fatalf("summary = %q want %q", got, tt.want)
			}
		})
	}
}

// stubStaleDiskDetection installs seams so detection sees the given VMs,
// holders, and running states without touching real processes.
func stubStaleDiskDetection(t *testing.T, vms map[string]string, holders map[string][]vmDiskHolder, running map[string]bool) {
	t.Helper()
	oldList := vmProcessListVMs
	oldHolders := vmDiskHoldersFor
	oldRunning := staleDiskVMRunning
	t.Cleanup(func() {
		vmProcessListVMs = oldList
		vmDiskHoldersFor = oldHolders
		staleDiskVMRunning = oldRunning
	})
	names := make([]string, 0, len(vms))
	for name := range vms {
		names = append(names, name)
	}
	sort.Strings(names)
	vmProcessListVMs = func() ([]vmconfig.Info, error) {
		infos := make([]vmconfig.Info, 0, len(names))
		for _, name := range names {
			infos = append(infos, vmconfig.Info{Name: name, Path: vms[name]})
		}
		return infos, nil
	}
	vmDiskHoldersFor = func(vmDir string) ([]vmDiskHolder, error) {
		return holders[vmDir], nil
	}
	staleDiskVMRunning = func(vmDir string) bool {
		return running[vmDir]
	}
}

func TestCollectStaleDiskLeases(t *testing.T) {
	vms := map[string]string{
		"default": "/vm/default.covevm",
		"busy":    "/vm/busy.covevm",
		"clean":   "/vm/clean.covevm",
	}
	holders := map[string][]vmDiskHolder{
		"/vm/default.covevm": {{PID: 53574, Command: "com.apple.Virtualization.VirtualMachine.xpc"}},
		"/vm/busy.covevm":    {{PID: 42, Command: "com.apple.Virtualization.VirtualMachine.xpc"}},
	}
	running := map[string]bool{"/vm/busy.covevm": true}
	stubStaleDiskDetection(t, vms, holders, running)

	leases := collectStaleDiskLeases()
	if len(leases) != 1 {
		t.Fatalf("leases = %+v, want 1", leases)
	}
	if leases[0].Name != "default" || leases[0].Holders[0].PID != 53574 {
		t.Fatalf("unexpected lease %+v", leases[0])
	}
}

func TestHostDoctorStaleDiskCheck(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		stubStaleDiskDetection(t, map[string]string{"clean": "/vm/clean.covevm"}, nil, nil)
		check := hostDoctorStaleDiskCheck()
		if check.Name != "stale-disk-lease" || check.Status != "pass" {
			t.Fatalf("check = %+v", check)
		}
	})
	t.Run("warn", func(t *testing.T) {
		stubStaleDiskDetection(t,
			map[string]string{"default": "/vm/default.covevm"},
			map[string][]vmDiskHolder{"/vm/default.covevm": {{PID: 53574}}},
			nil,
		)
		check := hostDoctorStaleDiskCheck()
		if check.Status != "warn" {
			t.Fatalf("status = %q, want warn", check.Status)
		}
		want := "default: disk held by stale VZ process (pid 53574) though VM is stopped"
		if !strings.Contains(check.Message, want) {
			t.Fatalf("message = %q, want contains %q", check.Message, want)
		}
		if !strings.Contains(check.Message, "cove doctor clear-stale-locks") {
			t.Fatalf("message missing recovery hint: %q", check.Message)
		}
	})
}

func TestRunClearStaleDiskLocksDryRun(t *testing.T) {
	stubStaleDiskDetection(t,
		map[string]string{"default": "/vm/default.covevm"},
		map[string][]vmDiskHolder{"/vm/default.covevm": {{PID: 53574, Command: "vz.xpc"}}},
		nil,
	)
	oldKill := staleDiskKill
	t.Cleanup(func() { staleDiskKill = oldKill })
	killed := false
	staleDiskKill = func(int, syscall.Signal) error { killed = true; return nil }

	var buf bytes.Buffer
	if err := runClearStaleDiskLocks(&buf, "", true); err != nil {
		t.Fatalf("runClearStaleDiskLocks: %v", err)
	}
	if killed {
		t.Fatal("dry run terminated a process")
	}
	if !strings.Contains(buf.String(), "would terminate pid 53574") {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestRunClearStaleDiskLocks(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, runLockFile)
	if err := os.WriteFile(lockPath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	stubStaleDiskDetection(t,
		map[string]string{"default": dir},
		map[string][]vmDiskHolder{dir: {{PID: 53574, Command: "vz.xpc"}}},
		nil,
	)
	oldKill := staleDiskKill
	oldLive := staleDiskProcessLiveFn
	t.Cleanup(func() {
		staleDiskKill = oldKill
		staleDiskProcessLiveFn = oldLive
	})
	var gotSignals []syscall.Signal
	staleDiskKill = func(pid int, sig syscall.Signal) error {
		gotSignals = append(gotSignals, sig)
		return nil
	}
	staleDiskProcessLiveFn = func(int) bool { return false }

	var buf bytes.Buffer
	if err := runClearStaleDiskLocks(&buf, "default", false); err != nil {
		t.Fatalf("runClearStaleDiskLocks: %v", err)
	}
	if len(gotSignals) != 1 || gotSignals[0] != syscall.SIGTERM {
		t.Fatalf("signals = %v, want single SIGTERM", gotSignals)
	}
	out := buf.String()
	if !strings.Contains(out, "terminated pid 53574") {
		t.Fatalf("output missing termination line: %q", out)
	}
	if !strings.Contains(out, "removed stale") {
		t.Fatalf("output missing lock removal: %q", out)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("run.lock not removed: %v", err)
	}
}

func TestRunClearStaleDiskLocksNone(t *testing.T) {
	stubStaleDiskDetection(t, map[string]string{"clean": "/vm/clean.covevm"}, nil, nil)
	var buf bytes.Buffer
	if err := runClearStaleDiskLocks(&buf, "missing", false); err != nil {
		t.Fatalf("runClearStaleDiskLocks: %v", err)
	}
	if !strings.Contains(buf.String(), `No stale VM disk locks found for "missing"`) {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestTerminateStaleHolderEscalates(t *testing.T) {
	oldKill := staleDiskKill
	oldLive := staleDiskProcessLiveFn
	oldPolls := staleDiskTermPolls
	oldInterval := staleDiskTermInterval
	t.Cleanup(func() {
		staleDiskKill = oldKill
		staleDiskProcessLiveFn = oldLive
		staleDiskTermPolls = oldPolls
		staleDiskTermInterval = oldInterval
	})
	staleDiskTermPolls = 2
	staleDiskTermInterval = 0
	var signals []syscall.Signal
	staleDiskKill = func(pid int, sig syscall.Signal) error {
		signals = append(signals, sig)
		return nil
	}
	staleDiskProcessLiveFn = func(int) bool { return true } // never dies

	if err := terminateStaleHolder(99); err != nil {
		t.Fatalf("terminateStaleHolder: %v", err)
	}
	if len(signals) != 2 || signals[0] != syscall.SIGTERM || signals[1] != syscall.SIGKILL {
		t.Fatalf("signals = %v, want SIGTERM then SIGKILL", signals)
	}
}
