package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stageTestVMForClean(t *testing.T, dir string) {
	t.Helper()
	files := []string{
		"disk.img",
		"aux.img",
		"hw.model",
		"machine.id",
		"boot-args.txt",
		".inject-succeeded",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test data"), 0644); err != nil {
			t.Fatalf("stage file %s: %v", f, err)
		}
	}
	stagingDir := filepath.Join(dir, ".provision")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		t.Fatalf("stage staging dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "stage.txt"), []byte("staged"), 0644); err != nil {
		t.Fatalf("stage staging file: %v", err)
	}
}

func assertCleanFilesExist(t *testing.T, dir string, wantExist bool) {
	t.Helper()
	files := []string{
		"disk.img",
		"aux.img",
		"hw.model",
		"machine.id",
		"boot-args.txt",
		".inject-succeeded",
		".provision",
	}
	for _, f := range files {
		path := filepath.Join(dir, f)
		_, err := os.Stat(path)
		exists := err == nil
		if exists != wantExist {
			t.Errorf("file %s exists = %v, want %v", f, exists, wantExist)
		}
	}
}

func TestCleanRunningVMRefuses(t *testing.T) {
	oldTimeout := cleanWaitNotRunningTimeout
	cleanWaitNotRunningTimeout = 50 * time.Millisecond
	t.Cleanup(func() { cleanWaitNotRunningTimeout = oldTimeout })

	home := t.TempDir()
	if realHome, err := filepath.EvalSymlinks(home); err == nil {
		home = realHome
	}
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".vz", "vms", "running-vm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if realDir, err := filepath.EvalSymlinks(dir); err == nil {
		dir = realDir
	}
	stageTestVMForClean(t, dir)

	// Mock running VM by listening on the control socket
	sock := GetControlSocketPathForVM(dir)
	if err := os.MkdirAll(filepath.Dir(sock), 0755); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer ln.Close()

	target := vmSelection{Directory: dir, Name: "running-vm"}

	// 1. Direct call to cleanVMForVM must refuse
	err = cleanVMForVM(target)
	if err == nil {
		t.Fatal("cleanVMForVM on running VM succeeded, want error")
	}
	if !strings.Contains(err.Error(), "currently running") {
		t.Fatalf("cleanVMForVM err = %v, want containing 'currently running'", err)
	}
	assertCleanFilesExist(t, dir, true)

	// 2. handleCleanCommand must refuse
	var stdout, stderr bytes.Buffer
	env := commandEnv{Stdout: &stdout, Stderr: &stderr}
	code := handleCleanCommand(env, []string{"-y", "-vm", "running-vm"})
	if code != 1 {
		t.Fatalf("handleCleanCommand on running VM exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "currently running") {
		t.Fatalf("stderr = %q, want containing 'currently running'", stderr.String())
	}
	assertCleanFilesExist(t, dir, true)
}

func TestCleanNonInteractiveWithoutYesFailsExit2(t *testing.T) {
	oldIsTerminal := confirmStdinIsTerminal
	oldStdin := confirmStdin
	oldStderr := confirmStderr
	t.Cleanup(func() {
		confirmStdinIsTerminal = oldIsTerminal
		confirmStdin = oldStdin
		confirmStderr = oldStderr
	})

	confirmStdinIsTerminal = func() bool { return false }
	var errBuf bytes.Buffer
	confirmStderr = &errBuf

	dir := t.TempDir()
	stageTestVMForClean(t, dir)

	oldVMDir := vmDir
	oldVMName := vmName
	vmDir = dir
	vmName = "test-vm"
	t.Cleanup(func() {
		vmDir = oldVMDir
		vmName = oldVMName
	})

	var stdout, stderr bytes.Buffer
	env := commandEnv{Stdout: &stdout, Stderr: &stderr}
	code := handleCleanCommand(env, nil)
	if code != 2 {
		t.Fatalf("handleCleanCommand exit code = %d, want 2", code)
	}
	wantMsg := "deletion requires confirmation; use -y/--yes in non-interactive environments"
	if !strings.Contains(errBuf.String(), wantMsg) {
		t.Errorf("confirmStderr = %q, want substring %q", errBuf.String(), wantMsg)
	}
	assertCleanFilesExist(t, dir, true)
}

func TestCleanWithYesRemovesFiles(t *testing.T) {
	for _, flag := range []string{"-y", "--yes", "-yes"} {
		t.Run(flag, func(t *testing.T) {
			dir := t.TempDir()
			stageTestVMForClean(t, dir)

			oldVMDir := vmDir
			oldVMName := vmName
			vmDir = dir
			vmName = "clean-test"
			t.Cleanup(func() {
				vmDir = oldVMDir
				vmName = oldVMName
			})

			var stdout, stderr bytes.Buffer
			env := commandEnv{Stdout: &stdout, Stderr: &stderr}
			code := handleCleanCommand(env, []string{flag})
			if code != 0 {
				t.Fatalf("handleCleanCommand(%s) exit code = %d, want 0; stderr: %s", flag, code, stderr.String())
			}
			assertCleanFilesExist(t, dir, false)
		})
	}
}

func TestCleanInteractiveRejectionAbortsExit1(t *testing.T) {
	oldIsTerminal := confirmStdinIsTerminal
	oldStdin := confirmStdin
	oldStderr := confirmStderr
	t.Cleanup(func() {
		confirmStdinIsTerminal = oldIsTerminal
		confirmStdin = oldStdin
		confirmStderr = oldStderr
	})

	confirmStdinIsTerminal = func() bool { return true }
	confirmStdin = strings.NewReader("n\n")
	var errBuf bytes.Buffer
	confirmStderr = &errBuf

	dir := t.TempDir()
	stageTestVMForClean(t, dir)

	oldVMDir := vmDir
	oldVMName := vmName
	vmDir = dir
	vmName = "test-vm"
	t.Cleanup(func() {
		vmDir = oldVMDir
		vmName = oldVMName
	})

	var stdout, stderr bytes.Buffer
	env := commandEnv{Stdout: &stdout, Stderr: &stderr}
	code := handleCleanCommand(env, nil)
	if code != 1 {
		t.Fatalf("handleCleanCommand exit code = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "aborted") {
		t.Errorf("confirmStderr = %q, want substring 'aborted'", errBuf.String())
	}
	assertCleanFilesExist(t, dir, true)
}

func TestCleanInteractiveConfirmationRemovesFiles(t *testing.T) {
	oldIsTerminal := confirmStdinIsTerminal
	oldStdin := confirmStdin
	oldStderr := confirmStderr
	t.Cleanup(func() {
		confirmStdinIsTerminal = oldIsTerminal
		confirmStdin = oldStdin
		confirmStderr = oldStderr
	})

	confirmStdinIsTerminal = func() bool { return true }
	confirmStdin = strings.NewReader("y\n")
	var errBuf bytes.Buffer
	confirmStderr = &errBuf

	dir := t.TempDir()
	stageTestVMForClean(t, dir)

	oldVMDir := vmDir
	oldVMName := vmName
	vmDir = dir
	vmName = "test-vm"
	t.Cleanup(func() {
		vmDir = oldVMDir
		vmName = oldVMName
	})

	var stdout, stderr bytes.Buffer
	env := commandEnv{Stdout: &stdout, Stderr: &stderr}
	code := handleCleanCommand(env, nil)
	if code != 0 {
		t.Fatalf("handleCleanCommand exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	assertCleanFilesExist(t, dir, false)
}
