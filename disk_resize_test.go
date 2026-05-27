package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestParseDiskResizeArgs(t *testing.T) {
	oldVMName := vmName
	t.Cleanup(func() { vmName = oldVMName })

	vmName = "selected"
	vm, size, err := parseDiskResizeArgs([]string{"128G"})
	if err != nil {
		t.Fatalf("parseDiskResizeArgs: %v", err)
	}
	if vm != "selected" || size != "128G" {
		t.Fatalf("parseDiskResizeArgs = %q, %q, want selected, 128G", vm, size)
	}

	vm, size, err = parseDiskResizeArgs([]string{"other", "96G"})
	if err != nil {
		t.Fatalf("parseDiskResizeArgs explicit: %v", err)
	}
	if vm != "other" || size != "96G" {
		t.Fatalf("parseDiskResizeArgs explicit = %q, %q, want other, 96G", vm, size)
	}
}

func TestResizeStoppedVMDiskGrowsPrimaryDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	deps := stoppedResizeTestDeps(emptyVMProcessRunner())
	vmDir := filepath.Join(vmconfig.BaseDir(), "grow")
	if err := os.MkdirAll(vmDir, 0700); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(vmDir, "disk.img")
	if err := os.WriteFile(disk, []byte("1234"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "aux.img"), []byte("aux"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "hw.model"), []byte("mac"), 0600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := resizeStoppedVMDisk(&out, "grow", "8B", deps); err != nil {
		t.Fatalf("resizeStoppedVMDisk: %v", err)
	}
	info, err := os.Stat(disk)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 8 {
		t.Fatalf("disk size = %d, want 8", info.Size())
	}
	if !strings.Contains(out.String(), "cove ctl -vm grow disk resize 0 8B") {
		t.Fatalf("output missing APFS next step:\n%s", out.String())
	}
}

func TestResizeStoppedVMDiskRejectsShrink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	deps := stoppedResizeTestDeps(emptyVMProcessRunner())
	vmDir := filepath.Join(vmconfig.BaseDir(), "shrink")
	if err := os.MkdirAll(vmDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "linux-disk.img"), []byte("1234"), 0600); err != nil {
		t.Fatal(err)
	}

	err := resizeStoppedVMDisk(ioDiscard{}, "shrink", "3B", deps)
	if err == nil || !strings.Contains(err.Error(), "can only grow") {
		t.Fatalf("resizeStoppedVMDisk shrink error = %v, want can only grow", err)
	}
}

func TestResizeStoppedVMDiskRejectsHeldRunLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	deps := stoppedResizeTestDeps(emptyVMProcessRunner())
	vmDir := filepath.Join(vmconfig.BaseDir(), "locked")
	if err := os.MkdirAll(vmDir, 0700); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(vmDir, "linux-disk.img")
	if err := os.WriteFile(disk, []byte("1234"), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireRunLock(vmDir)
	if err != nil {
		t.Fatalf("AcquireRunLock: %v", err)
	}
	defer lock.Release()

	err = resizeStoppedVMDisk(ioDiscard{}, "locked", "8B", deps)
	if err == nil || !strings.Contains(err.Error(), "is running") {
		t.Fatalf("resizeStoppedVMDisk locked error = %v, want running refusal", err)
	}
	info, err := os.Stat(disk)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 4 {
		t.Fatalf("disk size = %d, want unchanged 4", info.Size())
	}
}

func TestResizeStoppedVMDiskRejectsLiveVMProcess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vmDir := filepath.Join(vmconfig.BaseDir(), "live-xpc")
	if err := os.MkdirAll(vmDir, 0700); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(vmDir, "disk.img")
	if err := os.WriteFile(disk, []byte("1234"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "aux.img"), []byte("aux"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "hw.model"), []byte("mac"), 0600); err != nil {
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
	deps := stoppedResizeTestDeps(fakeVMProcessRunner{
		out: map[string][]byte{
			"ps\x00-axo\x00pid=,ppid=,command=":  []byte(" 202 1 com.apple.Virtualization.VirtualMachine.xpc\n"),
			"lsof\x00-nP\x00-Fpcfn\x00-p\x00202": []byte("p202\nn" + disk + "\n"),
		},
	})

	err := resizeStoppedVMDisk(ioDiscard{}, "live-xpc", "8B", deps)
	if err == nil || !strings.Contains(err.Error(), "appears to be running in PID 202") {
		t.Fatalf("resizeStoppedVMDisk live process error = %v, want PID refusal", err)
	}
	info, err := os.Stat(disk)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 4 {
		t.Fatalf("disk size = %d, want unchanged 4", info.Size())
	}
}

func TestResizeStoppedVMDiskRejectsOpenDiskImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	deps := stoppedResizeTestDeps(emptyVMProcessRunner())
	deps.fileHolders = func(string) ([]int, error) {
		return []int{303}, nil
	}
	vmDir := filepath.Join(vmconfig.BaseDir(), "open-disk")
	if err := os.MkdirAll(vmDir, 0700); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(vmDir, "linux-disk.img")
	if err := os.WriteFile(disk, []byte("1234"), 0600); err != nil {
		t.Fatal(err)
	}

	err := resizeStoppedVMDisk(ioDiscard{}, "open-disk", "8B", deps)
	if err == nil || !strings.Contains(err.Error(), "disk image is open by PID 303") {
		t.Fatalf("resizeStoppedVMDisk open disk error = %v, want PID refusal", err)
	}
	info, err := os.Stat(disk)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 4 {
		t.Fatalf("disk size = %d, want unchanged 4", info.Size())
	}
}

func stoppedResizeTestDeps(runner vmProcessRunner) stoppedDiskResizeDeps {
	return stoppedDiskResizeDeps{
		acquireRunLock: AcquireRunLock,
		processes:      commandVMProcessCollector{runner: runner},
		fileHolders: func(string) ([]int, error) {
			return nil, nil
		},
	}
}

func emptyVMProcessRunner() vmProcessRunner {
	return fakeVMProcessRunner{
		out: map[string][]byte{
			"ps\x00-axo\x00pid=,ppid=,command=": nil,
		},
	}
}
