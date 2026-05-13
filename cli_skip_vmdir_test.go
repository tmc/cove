package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// TestSubcommandSkipsVMDir guards the allowlist of subcommands that must boot
// without creating ~/.vz/vms entries during startup.
func TestSubcommandSkipsVMDir(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"helper bare", []string{"helper"}, true},
		{"helper daemon", []string{"helper", "daemon"}, true},
		{"helper status", []string{"helper", "status"}, true},
		{"list is global", []string{"list"}, true},
		{"ls is global", []string{"ls"}, true},
		{"cp uses control socket", []string{"cp"}, true},
		{"ctl uses control socket", []string{"ctl"}, true},
		{"logs is read only", []string{"logs"}, true},
		{"status is read only", []string{"status"}, true},
		{"storage is global census", []string{"storage"}, true},
		{"vzscript defers VM resolution", []string{"vzscript", "run", "recipe"}, true},
		{"version", []string{"version"}, true},
		{"vm tree", []string{"vm", "tree"}, true},
		{"vm tree extra args still skips startup VM dir", []string{"vm", "tree", "extra"}, true},
		{"run is not allowlisted", []string{"run"}, false},
		{"run with explicit vm skips startup VM dir", []string{"run", "-vm", "missing"}, true},
		{"run fork-from skips startup VM dir", []string{"run", "-fork-from", "missing:latest"}, true},
		{"run fork-from after other flags skips startup VM dir", []string{"run", "-headless", "-fork-from=missing:latest"}, true},
		{"run after -- does not inspect fork-from", []string{"run", "--", "-fork-from", "missing:latest"}, false},
		{"install is not allowlisted", []string{"install"}, false},
		{"vm is not allowlisted", []string{"vm", "list"}, false},
		{"unknown skips startup VM dir", []string{"banana"}, true},
		{"help is handled before startup VM dir", []string{"help"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subcommandSkipsVMDir(tt.args); got != tt.want {
				t.Errorf("subcommandSkipsVMDir(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestSubcommandSkipsVMDirRunWithGlobalVM(t *testing.T) {
	oldVMName := vmName
	t.Cleanup(func() { vmName = oldVMName })
	vmName = "missing-run-global"
	if got := subcommandSkipsVMDir([]string{"run", "-headless"}); !got {
		t.Fatal("subcommandSkipsVMDir(run with global vmName) = false, want true")
	}
}

func TestUnknownCommandWithGlobalVMDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	for _, tt := range []struct {
		name string
		args []string
		vm   string
	}{
		{"gui status", []string{"gui", "status"}, "missing-gui-status"},
		{"vnc status", []string{"vnc", "status"}, "missing-vnc-status"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			cmd := exec.Command(bin, append([]string{"-vm", tt.vm}, tt.args...)...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("unknown command succeeded\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			}
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("run unknown command: %v", err)
			}
			if exitErr.ExitCode() != 2 {
				t.Fatalf("exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "unknown command") {
				t.Fatalf("stderr missing unknown command:\n%s", stderr.String())
			}
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", tt.vm)); !os.IsNotExist(err) {
				t.Fatalf("VM dir stat = %v, want not exist", err)
			}
		})
	}
}

func TestStorageWithGlobalVMDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()
	vm := "missing-storage-vm"
	cmd := exec.Command(bin, "-vm", vm, "storage", "census")
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("storage command failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".vz", "vms", vm)); !os.IsNotExist(err) {
		t.Fatalf("storage VM dir stat = %v, want not exist", err)
	}
}

func TestListWithGlobalVMDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()
	vm := "missing-list-vm"
	cmd := exec.Command(bin, "-vm", vm, "list")
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("list command failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".vz", "vms", vm)); !os.IsNotExist(err) {
		t.Fatalf("list VM dir stat = %v, want not exist", err)
	}
}

func TestRunWithMissingVMDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()
	vm := "missing-run-vm"
	cmd := exec.Command(bin, "run", "-vm", vm, "-headless", "-start-timeout", "1s")
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("run missing VM succeeded\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run missing VM: %v", err)
	}
	if exitErr.ExitCode() == 0 {
		t.Fatalf("exit = 0, want nonzero\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `no VM named "missing-run-vm"`) {
		t.Fatalf("stderr missing missing-VM diagnostic:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".vz", "vms", vm)); !os.IsNotExist(err) {
		t.Fatalf("run missing VM dir stat = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(err) {
		t.Fatalf("default VM dir stat = %v, want not exist", err)
	}
}

func TestRerunVMDirForPostCommandSkipsStorage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "missing-post-storage-vm"
	vmDir = ""
	if code := rerunVMDirForPostCommand("storage", nil); code != 0 {
		t.Fatalf("rerunVMDirForPostCommand(storage) = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), vmName)); !os.IsNotExist(err) {
		t.Fatalf("storage VM dir stat = %v, want not exist", err)
	}
}

func TestRerunVMDirForPostCommandSkipsRunForkFrom(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "missing-run-fork-child"
	vmDir = ""
	if code := rerunVMDirForPostCommand("run", []string{"-fork-from", "missing:image"}); code != 0 {
		t.Fatalf("rerunVMDirForPostCommand(run -fork-from) = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), vmName)); !os.IsNotExist(err) {
		t.Fatalf("run fork-from VM dir stat = %v, want not exist", err)
	}
}

func TestRerunVMDirForPostCommandRunRequiresExistingVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "missing-post-run-vm"
	vmDir = ""
	if code := rerunVMDirForPostCommand("run", []string{"-vm", vmName, "-headless"}); code == 0 {
		t.Fatal("rerunVMDirForPostCommand(run missing) = 0, want nonzero")
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), vmName)); !os.IsNotExist(err) {
		t.Fatalf("run missing VM dir stat = %v, want not exist", err)
	}
}

func TestRerunVMDirForPostCommandRunAcceptsExistingVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "existing-post-run-vm"
	vmDir = ""
	dir := filepath.Join(vmconfig.BaseDir(), vmName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := rerunVMDirForPostCommand("run", []string{"-vm", vmName, "-headless"}); code != 0 {
		t.Fatalf("rerunVMDirForPostCommand(run existing) = %d, want 0", code)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := filepath.EvalSymlinks(vmDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("vmDir = %q, want %q", got, want)
	}
}

func TestRerunVMDirForPostCommandSkipsUnknownCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "missing-unknown-command"
	vmDir = ""
	if code := rerunVMDirForPostCommand("gui", []string{"status"}); code != 0 {
		t.Fatalf("rerunVMDirForPostCommand(unknown) = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), vmName)); !os.IsNotExist(err) {
		t.Fatalf("unknown command VM dir stat = %v, want not exist", err)
	}
}

func TestRerunVMDirForPostCommandSkipsControlSocketCommands(t *testing.T) {
	for _, tt := range []struct {
		name string
		cmd  string
		args []string
		vm   string
	}{
		{"cp", "cp", []string{"source.txt", "missing-cp-vm:/tmp/source.txt"}, "missing-cp-vm"},
		{"ctl", "ctl", []string{"status"}, "missing-ctl-vm"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			oldVMName, oldVMDir := vmName, vmDir
			t.Cleanup(func() {
				vmName, vmDir = oldVMName, oldVMDir
			})
			vmName = tt.vm
			vmDir = ""
			if code := rerunVMDirForPostCommand(tt.cmd, tt.args); code != 0 {
				t.Fatalf("rerunVMDirForPostCommand(%s) = %d, want 0", tt.cmd, code)
			}
			if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), tt.vm)); !os.IsNotExist(err) {
				t.Fatalf("%s VM dir stat = %v, want not exist", tt.cmd, err)
			}
		})
	}
}
