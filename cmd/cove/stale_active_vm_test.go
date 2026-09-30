package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestResolveTargetVM_StaleActiveVMWithRunningVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Create a valid VM
	runningDir := writeTestVM(t, "running-box")

	// Make running-box appear running by listening on its control socket
	sockPath := GetControlSocketPathForVM(runningDir)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0755); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen on control socket: %v", err)
	}
	defer l.Close()

	altSock := GetControlSocketPathForVM(filepath.Join(vmconfig.BaseDir(), "running-box"))
	if altSock != sockPath {
		_ = os.MkdirAll(filepath.Dir(altSock), 0755)
		if l2, err := net.Listen("unix", altSock); err == nil {
			defer l2.Close()
		}
	}

	// Point active VM link (~/.vz/current) to a missing VM bundle
	linkPath := vmconfig.CurrentLink()
	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	missingTarget := filepath.Join(vmconfig.BaseDir(), "missing-box.covevm")
	if err := os.Symlink(missingTarget, linkPath); err != nil {
		t.Fatalf("symlink missing VM: %v", err)
	}

	var stderrBuf bytes.Buffer
	name, dir, err := resolveTargetVM(VMResolveOptions{
		Command: "test",
		Stderr:  &stderrBuf,
	})
	if err != nil {
		t.Fatalf("resolveTargetVM error = %v, want fallback to running VM", err)
	}
	if name != "running-box" || vmconfig.NameForPath(dir) != "running-box" {
		t.Errorf("resolveTargetVM = (%q, %q), want name running-box", name, dir)
	}

	out := stderrBuf.String()
	if !strings.Contains(out, "warning: active VM pointer is stale") {
		t.Errorf("stderr missing stale active VM warning:\n%s", out)
	}
	if !strings.Contains(out, "cove vm set <name>") {
		t.Errorf("stderr missing suggestion to run 'cove vm set <name>':\n%s", out)
	}
}

func TestResolveTargetVM_StaleActiveVMNoRunningVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Point active VM link (~/.vz/current) to a missing VM bundle
	linkPath := vmconfig.CurrentLink()
	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	missingTarget := filepath.Join(vmconfig.BaseDir(), "missing-box.covevm")
	if err := os.Symlink(missingTarget, linkPath); err != nil {
		t.Fatalf("symlink missing VM: %v", err)
	}

	var stderrBuf bytes.Buffer
	_, _, err := resolveTargetVM(VMResolveOptions{
		Command: "test",
		Stderr:  &stderrBuf,
	})
	if err == nil {
		t.Fatal("resolveTargetVM succeeded with stale active VM and no running VMs, want error")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "active VM pointer is stale") {
		t.Errorf("error missing 'active VM pointer is stale':\n%s", errStr)
	}
	if !strings.Contains(errStr, "cove vm set <name>") {
		t.Errorf("error missing 'cove vm set <name>':\n%s", errStr)
	}
}

func TestDoctorReportsDanglingSymlinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	base := vmconfig.BaseDir()
	bundles := vmconfig.BundleDir()
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundles, 0755); err != nil {
		t.Fatal(err)
	}

	// Create dangling symlinks in ~/.vz/vms and ~/.vz/covevms
	danglingVMLink := filepath.Join(base, "dangling-vm")
	if err := os.Symlink(filepath.Join(base, "missing.covevm"), danglingVMLink); err != nil {
		t.Fatal(err)
	}
	danglingBundleLink := filepath.Join(bundles, "dangling.covevm")
	if err := os.Symlink(filepath.Join(base, "missing.covevm"), danglingBundleLink); err != nil {
		t.Fatal(err)
	}

	var stdoutBuf bytes.Buffer
	_ = handleVerifyWithOutput(nil, &stdoutBuf)

	out := stdoutBuf.String()
	if !strings.Contains(out, "Dangling VM symlinks") {
		t.Errorf("cove doctor output missing 'Dangling VM symlinks':\n%s", out)
	}
	if !strings.Contains(out, "dangling-vm") {
		t.Errorf("cove doctor output missing 'dangling-vm':\n%s", out)
	}
	if !strings.Contains(out, "dangling.covevm") {
		t.Errorf("cove doctor output missing 'dangling.covevm':\n%s", out)
	}
	if !strings.Contains(out, "cove doctor --fix") {
		t.Errorf("cove doctor output missing suggestion 'cove doctor --fix':\n%s", out)
	}

	// Also verify cove doctor host reports the dangling symlinks check
	check := hostDoctorDanglingSymlinksCheck()
	if check.Status != "warn" {
		t.Errorf("hostDoctorDanglingSymlinksCheck() status = %q, want warn", check.Status)
	}
	if !strings.Contains(check.Message, "cove doctor --fix") {
		t.Errorf("hostDoctorDanglingSymlinksCheck() message = %q, want mention of 'cove doctor --fix'", check.Message)
	}
}

func TestDoctorFixRemovesDanglingSymlinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	base := vmconfig.BaseDir()
	bundles := vmconfig.BundleDir()
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundles, 0755); err != nil {
		t.Fatal(err)
	}

	// Create dangling symlinks in ~/.vz/vms and ~/.vz/covevms
	danglingVMLink := filepath.Join(base, "dangling-vm")
	if err := os.Symlink(filepath.Join(base, "missing.covevm"), danglingVMLink); err != nil {
		t.Fatal(err)
	}
	danglingBundleLink := filepath.Join(bundles, "dangling.covevm")
	if err := os.Symlink(filepath.Join(base, "missing.covevm"), danglingBundleLink); err != nil {
		t.Fatal(err)
	}

	oldIsTerminal, oldStdin, oldStderr := confirmStdinIsTerminal, confirmStdin, confirmStderr
	t.Cleanup(func() {
		confirmStdinIsTerminal, confirmStdin, confirmStderr = oldIsTerminal, oldStdin, oldStderr
	})
	confirmStderr = io.Discard

	// Without a terminal to confirm, --fix must not remove anything.
	confirmStdinIsTerminal = func() bool { return false }
	var stdoutBuf bytes.Buffer
	if err := handleVerifyWithOutput([]string{"--fix"}, &stdoutBuf); err != nil {
		t.Fatalf("handleVerifyWithOutput(--fix) error = %v", err)
	}
	if _, err := os.Lstat(danglingVMLink); err != nil {
		t.Fatalf("--fix without confirmation removed %s: %v", danglingVMLink, err)
	}

	confirmStdinIsTerminal = func() bool { return true }
	confirmStdin = strings.NewReader("y\n")
	stdoutBuf.Reset()
	if err := handleVerifyWithOutput([]string{"--fix"}, &stdoutBuf); err != nil {
		t.Fatalf("handleVerifyWithOutput(--fix) error = %v", err)
	}

	out := stdoutBuf.String()
	if !strings.Contains(out, "removed:") || !strings.Contains(out, "angling") {
		t.Errorf("cove doctor --fix output missing removal notice:\n%s", out)
	}

	// Verify dangling links were actually removed from filesystem
	if _, err := os.Lstat(danglingVMLink); !os.IsNotExist(err) {
		t.Errorf("dangling VM link %s still exists: err = %v", danglingVMLink, err)
	}
	if _, err := os.Lstat(danglingBundleLink); !os.IsNotExist(err) {
		t.Errorf("dangling bundle link %s still exists: err = %v", danglingBundleLink, err)
	}

	// Verify host doctor check passes now
	check := hostDoctorDanglingSymlinksCheck()
	if check.Status != "pass" {
		t.Errorf("hostDoctorDanglingSymlinksCheck() post-fix status = %q, want pass", check.Status)
	}
}
