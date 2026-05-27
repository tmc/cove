package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

type fakeVMProcessRunner struct {
	out map[string][]byte
	err map[string]error
}

func (r fakeVMProcessRunner) Run(name string, args ...string) ([]byte, error) {
	key := name + "\x00" + strings.Join(args, "\x00")
	return r.out[key], r.err[key]
}

func withVMProcessHooks(t *testing.T, list func() ([]vmconfig.Info, error), info func(string) (RuntimeServerInfo, bool)) {
	t.Helper()

	oldList := vmProcessListVMs
	oldInfo := vmProcessServerInfo
	vmProcessListVMs = list
	vmProcessServerInfo = info
	t.Cleanup(func() {
		vmProcessListVMs = oldList
		vmProcessServerInfo = oldInfo
	})
}

func TestCollectVMProcessesMapsCoveVMDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	baseDir := vmconfig.BaseDir()
	validDir := filepath.Join(baseDir, "managed")
	orphanDir := filepath.Join(baseDir, "stale")
	if err := os.MkdirAll(validDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"disk.img", "aux.img"} {
		if err := os.WriteFile(filepath.Join(validDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(orphanDir, "disk.img"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	withVMProcessHooks(t,
		func() ([]vmconfig.Info, error) {
			return nil, nil
		},
		func(string) (RuntimeServerInfo, bool) {
			return RuntimeServerInfo{}, false
		},
	)

	runner := fakeVMProcessRunner{
		out: map[string][]byte{
			"ps\x00-axo\x00pid=,ppid=,command=": []byte(`
 101 11 /System/Library/Frameworks/Virtualization.framework/Versions/A/XPCServices/com.apple.Virtualization.VirtualMachine.xpc
 102 12 com.apple.Virtualization.VirtualMachine.xpc
 103 13 Virtual Machine Service
 999 /usr/bin/other
`),
			"lsof\x00-nP\x00-Fpcfn\x00-p\x00101": []byte("p101\nccom.apple.Virtualization.VirtualMachine.xpc\nn" + filepath.Join(validDir, "disk.img") + "\nn/private/tmp/other\n"),
			"lsof\x00-nP\x00-Fpcfn\x00-p\x00102": []byte("p102\nn" + filepath.Join(orphanDir, "disk.img") + "\n"),
			"lsof\x00-nP\x00-Fpcfn\x00-p\x00103": []byte("p103\nn/private/tmp/unrelated\n"),
		},
	}

	procs, err := collectVMProcessesWithCollector(baseDir, commandVMProcessCollector{runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 3 {
		t.Fatalf("processes = %d, want 3: %#v", len(procs), procs)
	}
	gotStatus := []string{procs[0].Status, procs[1].Status, procs[2].Status}
	if want := []string{"managed", "orphan", "unmanaged"}; !reflect.DeepEqual(gotStatus, want) {
		t.Fatalf("status = %#v, want %#v", gotStatus, want)
	}
	if got := procs[0].VMDirs; !reflect.DeepEqual(got, []string{validDir}) {
		t.Fatalf("managed dirs = %#v, want %q", got, validDir)
	}
	if got := procs[1].VMDirs; !reflect.DeepEqual(got, []string{orphanDir}) {
		t.Fatalf("orphan dirs = %#v, want %q", got, orphanDir)
	}
	if len(procs[2].OpenFiles) != 0 {
		t.Fatalf("unmanaged open files = %#v, want none", procs[2].OpenFiles)
	}
}

func TestCollectVMProcessesCorrelatesServerInfoOwnerPID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	baseDir := vmconfig.BaseDir()
	legacyDir := filepath.Join(filepath.Dir(baseDir), "hermes")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"disk.img", "aux.img"} {
		if err := os.WriteFile(filepath.Join(legacyDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	withVMProcessHooks(t,
		func() ([]vmconfig.Info, error) {
			return []vmconfig.Info{{Name: "hermes", Path: legacyDir}}, nil
		},
		func(sock string) (RuntimeServerInfo, bool) {
			if !strings.HasSuffix(sock, "/control.sock") {
				t.Fatalf("server-info socket = %q", sock)
			}
			return RuntimeServerInfo{PID: 78347, VMDir: legacyDir, SocketPath: filepath.Join(legacyDir, "control.sock")}, true
		},
	)
	runner := fakeVMProcessRunner{
		out: map[string][]byte{
			"ps\x00-axo\x00pid=,ppid=,command=":    []byte(" 78358 78347 com.apple.Virtualization.VirtualMachine.xpc\n"),
			"lsof\x00-nP\x00-Fpcfn\x00-p\x0078358": []byte("p78358\nn/private/tmp/not-cove\n"),
		},
	}
	procs, err := collectVMProcessesWithCollector(baseDir, commandVMProcessCollector{runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 1 {
		t.Fatalf("processes = %#v, want one", procs)
	}
	if procs[0].Status != "managed" || procs[0].Source != "server-info" {
		t.Fatalf("process = %#v, want managed via server-info", procs[0])
	}
	if !reflect.DeepEqual(procs[0].VMDirs, []string{legacyDir}) {
		t.Fatalf("vm dirs = %#v, want %q", procs[0].VMDirs, legacyDir)
	}
}

func TestCollectVMProcessesMapsCanonicalLegacyDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	baseDir := vmconfig.BaseDir()
	legacyDir := filepath.Join(filepath.Dir(baseDir), "legacy")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"disk.img", "aux.img"} {
		if err := os.WriteFile(filepath.Join(legacyDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	withVMProcessHooks(t,
		func() ([]vmconfig.Info, error) {
			return []vmconfig.Info{{Name: "legacy", Path: filepath.Join(baseDir, "legacy")}}, nil
		},
		func(string) (RuntimeServerInfo, bool) {
			return RuntimeServerInfo{VMDir: legacyDir}, true
		},
	)
	runner := fakeVMProcessRunner{
		out: map[string][]byte{
			"ps\x00-axo\x00pid=,ppid=,command=":  []byte(" 201 20 com.apple.Virtualization.VirtualMachine.xpc\n"),
			"lsof\x00-nP\x00-Fpcfn\x00-p\x00201": []byte("p201\nn" + filepath.Join(legacyDir, "disk.img") + "\n"),
		},
	}
	procs, err := collectVMProcessesWithCollector(baseDir, commandVMProcessCollector{runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 1 || procs[0].Status != "managed" || procs[0].Source != "open-files" {
		t.Fatalf("processes = %#v, want managed from canonical open file", procs)
	}
}

func TestWriteVMProcessReportIncludesSafeNextSteps(t *testing.T) {
	procs := []vmProcessInfo{
		{PID: 101, Status: "managed", Source: "open-files", VMDirs: []string{"/tmp/vms/managed"}, OpenFiles: []string{"/tmp/vms/managed/disk.img"}},
		{PID: 102, Status: "unmanaged"},
		{PID: 103, Status: "orphan", VMDirs: []string{"/tmp/vms/stale"}, OpenFiles: []string{"/tmp/vms/stale/disk.img"}},
	}
	var buf bytes.Buffer
	helpers := []helperProcessInfo{{PID: 201, PPID: 1, CPU: "19.7", RSSKB: 12345, Command: "/usr/local/libexec/cove-helper"}}
	writeVMProcessReport(&buf, "/tmp/vms", procs, helpers, nil)
	out := buf.String()
	for _, want := range []string{
		"Apple Virtualization VM processes",
		"101",
		"managed",
		"102",
		"unmanaged",
		"confirm the owner with `ps -p 102 -o pid,ppid,user,command`",
		"invalid cove VM directory",
		"before moving or deleting it",
		"cove-helper process",
		"Install state:",
		"RSS(KiB)",
		"201",
		"19.7",
		"not owned by a specific VM",
		"before stopping it",
		"cove helper status",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}

func TestCollectVMProcessesKeepsOpenFilesFailureNonFatal(t *testing.T) {
	withVMProcessHooks(t,
		func() ([]vmconfig.Info, error) {
			return nil, nil
		},
		func(string) (RuntimeServerInfo, bool) {
			return RuntimeServerInfo{}, false
		},
	)
	runner := fakeVMProcessRunner{
		out: map[string][]byte{
			"ps\x00-axo\x00pid=,ppid=,command=": []byte(" 101 1 com.apple.Virtualization.VirtualMachine.xpc\n"),
		},
		err: map[string]error{
			"lsof\x00-nP\x00-Fpcfn\x00-p\x00101": errors.New("permission denied"),
		},
	}
	procs, err := collectVMProcessesWithCollector("/tmp/vms", commandVMProcessCollector{runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 1 || procs[0].Status != "unmanaged" || procs[0].OpenFilesErr == nil {
		t.Fatalf("process = %#v, want unmanaged with open-files error", procs)
	}
	var buf bytes.Buffer
	writeVMProcessReport(&buf, "/tmp/vms", procs, nil, nil)
	if got := buf.String(); !strings.Contains(got, "not visible (permission denied)") {
		t.Fatalf("report missing open-files failure:\n%s", got)
	}
}

func TestCollectHelperProcesses(t *testing.T) {
	runner := fakeVMProcessRunner{
		out: map[string][]byte{
			"ps\x00-axo\x00pid=,ppid=,pcpu=,rss=,command=": []byte(`
 301 1 20.5 895968 /usr/local/libexec/cove-helper
 302 1 0.0 1024 /usr/bin/other
303 1 1.2 2048 (cove-helper)
 304 1 0.3 4096 /tmp/cove helper daemon
`),
		},
	}
	helpers, err := collectHelperProcesses(runner)
	if err != nil {
		t.Fatal(err)
	}
	if len(helpers) != 3 {
		t.Fatalf("helpers = %#v, want 3", helpers)
	}
	if helpers[0].PID != 301 || helpers[0].PPID != 1 || helpers[0].CPU != "20.5" || helpers[0].RSSKB != 895968 {
		t.Fatalf("helper[0] = %#v", helpers[0])
	}
	if helpers[1].PID != 303 {
		t.Fatalf("helper[1] = %#v, want pid 303", helpers[1])
	}
	if helpers[2].PID != 304 {
		t.Fatalf("helper[2] = %#v, want pid 304", helpers[2])
	}
}

func TestWriteVMProcessReportIncludesHelperWhenNoVMProcesses(t *testing.T) {
	var buf bytes.Buffer
	writeVMProcessReport(&buf, "/tmp/vms", nil, []helperProcessInfo{
		{PID: 401, PPID: 1, CPU: "0.0", RSSKB: 2048, Command: "/usr/local/libexec/cove-helper"},
	}, nil)
	out := buf.String()
	for _, want := range []string{
		"No Apple Virtualization VM XPC processes found.",
		"cove-helper process",
		"401",
		"Helper is active",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}
