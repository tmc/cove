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
		{"agent-upgrade defers VM resolution", []string{"agent-upgrade"}, true},
		{"upgrade-agent defers VM resolution", []string{"upgrade-agent"}, true},
		{"commands is global inventory", []string{"commands"}, true},
		{"config defers VM resolution", []string{"config", "export", "--help"}, true},
		{"diff is global image metadata", []string{"diff", "--json", "missing-a:latest", "missing-b:latest"}, true},
		{"provision-agent defers VM resolution", []string{"provision-agent"}, true},
		{"inject-agent defers VM resolution", []string{"inject-agent"}, true},
		{"verify defers VM resolution", []string{"verify"}, true},
		{"doctor defers VM resolution", []string{"doctor"}, true},
		{"shared-folder read path defers VM resolution", []string{"shared-folder", "list"}, true},
		{"shared-folders alias defers VM resolution", []string{"shared-folders", "list"}, true},
		{"list is global", []string{"list"}, true},
		{"ls is global", []string{"ls"}, true},
		{"image list is global", []string{"image", "list", "--json"}, true},
		{"cp uses control socket", []string{"cp"}, true},
		{"ctl uses control socket", []string{"ctl"}, true},
		{"logs is read only", []string{"logs"}, true},
		{"pins is global metadata", []string{"pins"}, true},
		{"recording list is read only", []string{"recording", "list"}, true},
		{"recordings alias is read only", []string{"recordings", "list"}, true},
		{"status is read only", []string{"status"}, true},
		{"security status is read only", []string{"security", "status"}, true},
		{"first-run is global help", []string{"first-run"}, true},
		{"gc is global cleanup", []string{"gc", "-dry-run"}, true},
		{"storage is global census", []string{"storage"}, true},
		{"trace status defers VM resolution", []string{"trace", "status", "vm"}, true},
		{"traces alias defers VM resolution", []string{"traces", "status", "vm"}, true},
		{"up defers VM dir until args validated", []string{"up"}, true},
		{"support is global diagnostics", []string{"support", "bundle"}, true},
		{"support-bundle alias is global diagnostics", []string{"support-bundle"}, true},
		{"vzscript defers VM resolution", []string{"vzscript", "run", "recipe"}, true},
		{"version", []string{"version"}, true},
		{"vm tree", []string{"vm", "tree"}, true},
		{"vm tree extra args still skips startup VM dir", []string{"vm", "tree", "extra"}, true},
		{"unknown vm subcommand skips startup VM dir", []string{"vm", "delet", "missing"}, true},
		{"vm delete skips startup VM dir", []string{"vm", "delete", "missing"}, true},
		{"rm alias skips startup VM dir", []string{"rm", "missing"}, true},
		{"remove alias skips startup VM dir", []string{"remove", "missing"}, true},
		{"destroy alias skips startup VM dir", []string{"destroy", "missing"}, true},
		{"run defers VM resolution", []string{"run"}, true},
		{"run with explicit vm skips startup VM dir", []string{"run", "-vm", "missing"}, true},
		{"run fork-from skips startup VM dir", []string{"run", "-fork-from", "missing:latest"}, true},
		{"run fork-from after other flags skips startup VM dir", []string{"run", "-headless", "-fork-from=missing:latest"}, true},
		{"run after -- still defers VM resolution", []string{"run", "--", "-fork-from", "missing:latest"}, true},
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

func TestUpMissingUserDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	for _, tt := range []struct {
		name string
		args []string
	}{
		{"bare up", []string{"up"}},
		{"headless up", []string{"up", "-headless"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			cmd := exec.Command(bin, tt.args...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("up missing user succeeded\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			}
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("up missing user: %v", err)
			}
			if exitErr.ExitCode() == 0 {
				t.Fatalf("exit = 0, want nonzero\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "missing required flag: -user") {
				t.Fatalf("stderr missing user diagnostic:\n%s", stderr.String())
			}
			if !strings.Contains(stderr.String(), "cove up -user <name>") {
				t.Fatalf("stderr missing up hint:\n%s", stderr.String())
			}
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(err) {
				t.Fatalf("default VM dir stat = %v, want not exist", err)
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

func TestGUIVNCAliasesWithMissingVMDoNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	for _, tt := range []struct {
		name     string
		args     []string
		globalVM bool
		vm       string
	}{
		{"gui status global vm", []string{"gui", "status"}, true, "missing-gui-status"},
		{"vnc status global vm", []string{"vnc", "status"}, true, "missing-vnc-status"},
		{"gui status local vm", []string{"gui", "status", "-vm", "missing-gui-status"}, false, "missing-gui-status"},
		{"vnc status local vm", []string{"vnc", "status", "-vm", "missing-vnc-status"}, false, "missing-vnc-status"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			args := append([]string{}, tt.args...)
			if tt.globalVM {
				args = append([]string{"-vm", tt.vm}, args...)
			}
			cmd := exec.Command(bin, args...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("alias command succeeded\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			}
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("run alias command: %v", err)
			}
			if exitErr.ExitCode() != 1 {
				t.Fatalf("exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout.String(), stderr.String())
			}
			for _, want := range []string{`no VM named`, "cove list", "cove up -user <name>"} {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr missing %q:\n%s", want, stderr.String())
				}
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

func TestGCDryRunDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()
	cmd := exec.Command(bin, "gc", "-dry-run")
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("gc -dry-run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(err) {
		t.Fatalf("default VM dir stat = %v, want not exist\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
}

func TestUnknownVMSubcommandDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()
	cmd := exec.Command(bin, "vm", "delet", "missing-vm")
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("unknown VM subcommand succeeded\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unknown VM subcommand: %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown vm command: delet") {
		t.Fatalf("stderr missing unknown vm command:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "missing-vm")); !os.IsNotExist(err) {
		t.Fatalf("missing VM dir stat = %v, want not exist\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(err) {
		t.Fatalf("default VM dir stat = %v, want not exist\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
}

func TestSafeDiscoveryWithGlobalVMDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	for _, tt := range []struct {
		name string
		args []string
	}{
		{"diff", []string{"diff", "--json", "missing-a:latest", "missing-b:latest"}},
		{"pins", []string{"pins"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			vm := "missing-" + tt.name + "-vm"
			cmd := exec.Command(bin, append([]string{"-vm", vm}, tt.args...)...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", vm)); !os.IsNotExist(err) {
				t.Fatalf("%s VM dir stat = %v, want not exist\nstdout:\n%s\nstderr:\n%s", tt.name, err, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(err) {
				t.Fatalf("%s default VM dir stat = %v, want not exist\nstdout:\n%s\nstderr:\n%s", tt.name, err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestVMDeleteMissingDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	for _, tt := range []struct {
		name string
		args []string
		vm   string
	}{
		{"vm delete", []string{"vm", "delete", "missing-delete-vm"}, "missing-delete-vm"},
		{"rm alias", []string{"rm", "missing-rm-vm"}, "missing-rm-vm"},
		{"remove alias", []string{"remove", "missing-remove-vm"}, "missing-remove-vm"},
		{"destroy alias", []string{"destroy", "missing-destroy-vm"}, "missing-destroy-vm"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			cmd := exec.Command(bin, tt.args...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("delete missing VM succeeded\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			}
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("delete missing VM: %v", err)
			}
			if exitErr.ExitCode() != 1 {
				t.Fatalf("exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "vm not found: "+tt.vm) {
				t.Fatalf("stderr missing useful not-found diagnostic:\n%s", stderr.String())
			}
			for _, want := range []string{"list VMs: cove list", "create a VM: cove up -user <name>"} {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr missing hint %q:\n%s", want, stderr.String())
				}
			}
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", tt.vm)); !os.IsNotExist(err) {
				t.Fatalf("missing VM dir stat = %v, want not exist\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(err) {
				t.Fatalf("default VM dir stat = %v, want not exist\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestAgentProvisionVerifyWithMissingVMDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	for _, tt := range []struct {
		name string
		args []string
		vm   string
		want string
	}{
		{"agent-upgrade global vm", []string{"-vm", "missing-agent-upgrade-global", "agent-upgrade"}, "missing-agent-upgrade-global", `no VM named "missing-agent-upgrade-global"`},
		{"agent-upgrade post vm", []string{"agent-upgrade", "-vm", "missing-agent-upgrade-post"}, "missing-agent-upgrade-post", `no VM named "missing-agent-upgrade-post"`},
		{"provision-agent global vm", []string{"-vm", "missing-provision-agent-global", "provision-agent"}, "missing-provision-agent-global", `no VM named "missing-provision-agent-global"`},
		{"verify global vm", []string{"-vm", "missing-verify-global", "verify"}, "missing-verify-global", "disk image not found"},
		{"verify post vm", []string{"verify", "-vm", "missing-verify-post"}, "missing-verify-post", `no VM named "missing-verify-post"`},
		{"doctor global vm", []string{"-vm", "missing-doctor-global", "doctor"}, "missing-doctor-global", "disk image not found"},
		{"doctor post vm", []string{"doctor", "-vm", "missing-doctor-post"}, "missing-doctor-post", `no VM named "missing-doctor-post"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			cmd := exec.Command(bin, tt.args...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("command succeeded\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String()+stdout.String(), tt.want) {
				t.Fatalf("output missing %q\nstdout:\n%s\nstderr:\n%s", tt.want, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", tt.vm)); !os.IsNotExist(err) {
				t.Fatalf("VM dir stat = %v, want not exist", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(err) {
				t.Fatalf("default VM dir stat = %v, want not exist", err)
			}
		})
	}
}

func TestSharedFolderWithMissingVMDoesNotCreateVMDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	for _, tt := range []struct {
		name string
		args []string
		vm   string
	}{
		{"list global vm", []string{"-vm", "missing-shared-list", "shared-folder", "list"}, "missing-shared-list"},
		{"status global vm", []string{"-vm", "missing-shared-status", "shared-folder", "status"}, "missing-shared-status"},
		{"pending positional vm", []string{"shared-folder", "pending", "missing-shared-pending"}, "missing-shared-pending"},
		{"mount global vm", []string{"-vm", "missing-shared-mount", "shared-folder", "mount"}, "missing-shared-mount"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			cmd := exec.Command(bin, tt.args...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", tt.vm)); !os.IsNotExist(err) {
				t.Fatalf("VM dir stat = %v, want not exist\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filepath.Join(home, ".vz", "vms", "default")); !os.IsNotExist(err) {
				t.Fatalf("default VM dir stat = %v, want not exist\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
		})
	}
}

func TestReadOnlyDiscoveryCommandsLeaveOnlyVMRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()
	commands := [][]string{
		{"vm", "tree", "--json"},
		{"storage", "census", "-human"},
		{"shared-folder", "list"},
		{"-vm", "missing-readonly-status", "shared-folder", "status"},
	}
	for _, args := range commands {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
	}
	vmsDir := filepath.Join(home, ".vz", "vms")
	entries, err := os.ReadDir(vmsDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", vmsDir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("read-only discovery left VM entries: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(vmsDir, "default")); !os.IsNotExist(err) {
		t.Fatalf("default VM dir stat = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(vmsDir, "missing-readonly-status")); !os.IsNotExist(err) {
		t.Fatalf("missing VM dir stat = %v, want not exist", err)
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
	if !strings.Contains(stderr.String(), "list VMs: cove list") {
		t.Fatalf("stderr missing list hint:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "create a VM: cove up -user <name>") {
		t.Fatalf("stderr missing create hint:\n%s", stderr.String())
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
