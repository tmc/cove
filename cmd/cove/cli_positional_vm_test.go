package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestCLIPositionalVMRunMissing(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	cmd := exec.Command(bin, "run", "bogus-vm")
	cmd.Env = doctorE2EEnv(home)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("cove run bogus-vm succeeded unexpectedly\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run bogus-vm failed without exit error: %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `no VM named "bogus-vm"`) {
		t.Fatalf("stderr = %q, want containing 'no VM named \"bogus-vm\"'", stderr.String())
	}
	if strings.Contains(stderr.String(), `"default"`) {
		t.Fatalf("stderr = %q, should not reference 'default' VM", stderr.String())
	}
}

func TestCLIPositionalVMRunGuiFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name string
		args []string
	}{
		{"flag after positional", []string{"foo", "-gui"}},
		{"flag before positional", []string{"-gui", "foo"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldVMName, oldVMDir := vmName, vmDir
			oldGui := guiMode
			t.Cleanup(func() {
				vmName, vmDir = oldVMName, oldVMDir
				guiMode = oldGui
			})

			vmName = ""
			vmDir = ""
			guiMode = false

			// Create target VM so applyPositionalVMTarget("run", "foo") succeeds
			dir := filepath.Join(vmconfig.BaseDir(), "foo")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
				t.Fatal(err)
			}

			if err := parseLateSubcommandArgs("run", tt.args); err != nil {
				t.Fatalf("parseLateSubcommandArgs(run, %v) = %v, want nil", tt.args, err)
			}
			if !guiMode {
				t.Errorf("guiMode = false, want true")
			}
			if vmName != "foo" {
				t.Errorf("vmName = %q, want 'foo'", vmName)
			}
		})
	}
}

func TestCLIPositionalVMInstallTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name string
		args []string
	}{
		{"force before positional", []string{"-force", "newvm"}},
		{"force after positional", []string{"newvm", "-force"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldVMName, oldVMDir := vmName, vmDir
			oldForce := forceInstall
			t.Cleanup(func() {
				vmName, vmDir = oldVMName, oldVMDir
				forceInstall = oldForce
			})

			vmName = ""
			vmDir = ""
			forceInstall = false

			if err := parseLateSubcommandArgs("install", tt.args); err != nil {
				t.Fatalf("parseLateSubcommandArgs(install, %v) = %v, want nil", tt.args, err)
			}
			if !forceInstall {
				t.Errorf("forceInstall = false, want true")
			}
			if vmName != "newvm" {
				t.Errorf("vmName = %q, want 'newvm'", vmName)
			}
			wantDir, err := filepath.EvalSymlinks(vmconfig.Path("newvm"))
			if err != nil {
				t.Fatal(err)
			}
			gotDir, err := filepath.EvalSymlinks(vmDir)
			if err != nil {
				t.Fatal(err)
			}
			if gotDir != wantDir {
				t.Errorf("vmDir = %q, want %q", gotDir, wantDir)
			}
		})
	}
}

func TestCLIPositionalVMTooManyArguments(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	tests := []struct {
		name string
		args []string
		sub  string
	}{
		{"run too many args", []string{"run", "foo", "bar"}, "run"},
		{"install too many args", []string{"install", "foo", "bar"}, "install"},
		{"clean too many args", []string{"clean", "foo", "bar"}, "clean"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(bin, tt.args...)
			cmd.Env = doctorE2EEnv(home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("%v succeeded unexpectedly\nstdout:\n%s\nstderr:\n%s", tt.args, stdout.String(), stderr.String())
			}
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("%v failed without exit error: %v", tt.args, err)
			}
			if exitErr.ExitCode() != 2 {
				t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "too many arguments") {
				t.Fatalf("stderr = %q, want containing 'too many arguments'", stderr.String())
			}
		})
	}
}

func TestCLIPositionalVMConflictingFlags(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	tests := []struct {
		name string
		args []string
	}{
		{"run conflicting -vm after", []string{"run", "-vm", "foo", "bar"}},
		{"run conflicting -vm before", []string{"-vm", "foo", "run", "bar"}},
		{"install conflicting -vm", []string{"install", "-vm", "foo", "bar"}},
		{"clean conflicting -vm after", []string{"clean", "-vm", "foo", "bar"}},
		{"clean conflicting -vm before", []string{"-vm", "foo", "clean", "bar"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(bin, tt.args...)
			cmd.Env = doctorE2EEnv(home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err == nil {
				t.Fatalf("%v succeeded unexpectedly\nstdout:\n%s\nstderr:\n%s", tt.args, stdout.String(), stderr.String())
			}
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("%v failed without exit error: %v", tt.args, err)
			}
			if exitErr.ExitCode() != 2 {
				t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "conflicting VM names") {
				t.Fatalf("stderr = %q, want containing 'conflicting VM names'", stderr.String())
			}
		})
	}
}

func TestCLIPositionalVMCleanMissing(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	cmd := exec.Command(bin, "clean", "-y", "bogus-vm")
	cmd.Env = doctorE2EEnv(home)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("clean bogus-vm succeeded unexpectedly\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("clean bogus-vm failed without exit error: %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `clean: no VM named "bogus-vm"`) {
		t.Fatalf("stderr = %q, want containing 'clean: no VM named \"bogus-vm\"'", stderr.String())
	}
}

func TestRunForkPositionalChild(t *testing.T) {
	oldParent, oldName, oldVM, oldDir := ephemeralForkParent, ephemeralForkName, vmName, vmDir
	t.Cleanup(func() {
		ephemeralForkParent, ephemeralForkName, vmName, vmDir = oldParent, oldName, oldVM, oldDir
	})
	ephemeralForkParent, ephemeralForkName, vmName, vmDir = "base", "", "", ""
	if err := applyPositionalVMTarget("run", "child"); err != nil {
		t.Fatal(err)
	}
	if ephemeralForkName != "child" || vmDir != "" || vmName != "" {
		t.Fatalf("fork name = %q, VM = %q, directory = %q", ephemeralForkName, vmName, vmDir)
	}
	if err := applyPositionalVMTarget("run", "other"); err == nil {
		t.Fatal("accepted conflicting child names")
	}
}
