//go:build integration && darwin && arm64

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func testVirtioFSReliability(t *testing.T, baseVM *testVM) {
	t.Run("cold-boot-mounted-share", func(t *testing.T) {
		skipMacOSRunningSourceClone(t, baseVM)

		cloneName := integrationCloneName(t.Name())
		if err := CloneVM(CloneOptions{Source: baseVM.name, Target: cloneName, Linked: true}); err != nil {
			t.Fatalf("CloneVM() error = %v", err)
		}
		vm := clonedTestVM(t, cloneName, baseVM.linux)

		hostDir := t.TempDir()
		writeVirtioFSTestTree(t, hostDir)
		entry, _, err := addSharedFolderEntry(vm.dir, hostDir, "cold-boot", false)
		if err != nil {
			t.Fatalf("addSharedFolderEntry(): %v", err)
		}

		startTestVM(t, vm)
		waitVMReadyTB(t, vm, integrationVMReadyTimeout(vm, false))

		if _, err := mountSharedFoldersInGuest(vm.dir, defaultSharedFoldersMountPoint); err != nil {
			t.Fatalf("mountSharedFoldersInGuest(): %v", err)
		}
		assertVirtioFSTestTreeVisible(t, vm, filepath.Join(defaultSharedFoldersMountPoint, entry.Tag))
	})

	t.Run("running-hot-apply", func(t *testing.T) {
		vm := cloneTestVM(t, baseVM)

		hostDir := t.TempDir()
		writeVirtioFSTestTree(t, hostDir)
		entry, _, err := addSharedFolderEntry(vm.dir, hostDir, "hot-apply", false)
		if err != nil {
			t.Fatalf("addSharedFolderEntry(): %v", err)
		}

		ctlDo(t, vm, &controlpb.ControlRequest{Type: "shared-folders-apply"})
		if _, err := mountSharedFoldersInGuest(vm.dir, defaultSharedFoldersMountPoint); err != nil {
			t.Fatalf("mountSharedFoldersInGuest(): %v", err)
		}
		assertVirtioFSTestTreeVisible(t, vm, filepath.Join(defaultSharedFoldersMountPoint, entry.Tag))
	})

	t.Run("pause-resume-mounted-share", func(t *testing.T) {
		vm := cloneTestVM(t, baseVM)

		hostDir := t.TempDir()
		writeVirtioFSTestTree(t, hostDir)
		entry, _, err := addSharedFolderEntry(vm.dir, hostDir, "pause-resume", false)
		if err != nil {
			t.Fatalf("addSharedFolderEntry(): %v", err)
		}

		ctlDo(t, vm, &controlpb.ControlRequest{Type: "shared-folders-apply"})
		if _, err := mountSharedFoldersInGuest(vm.dir, defaultSharedFoldersMountPoint); err != nil {
			t.Fatalf("mountSharedFoldersInGuest(): %v", err)
		}

		guestRoot := filepath.Join(defaultSharedFoldersMountPoint, entry.Tag)
		assertVirtioFSTestTreeVisible(t, vm, guestRoot)

		status := statusResponse(t, vm)
		if !status.GetCanPause() {
			t.Skip("pause not supported for this VM")
		}
		ctlDo(t, vm, &controlpb.ControlRequest{Type: "pause"})
		waitVMState(t, vm, "paused", 30*time.Second)
		ctlDo(t, vm, &controlpb.ControlRequest{Type: "resume"})
		waitVMState(t, vm, "running", 30*time.Second)
		waitVMReady(t, vm, integrationVMReadyTimeout(vm, false))

		assertVirtioFSTestTreeVisible(t, vm, guestRoot)
	})

	t.Run("resumed-hot-apply-is-bounded", func(t *testing.T) {
		vm := cloneTestVM(t, baseVM)

		status := statusResponse(t, vm)
		if !status.GetCanPause() {
			t.Skip("pause not supported for this VM")
		}
		ctlDo(t, vm, &controlpb.ControlRequest{Type: "pause"})
		waitVMState(t, vm, "paused", 30*time.Second)
		ctlDo(t, vm, &controlpb.ControlRequest{Type: "resume"})
		waitVMState(t, vm, "running", 30*time.Second)
		waitVMReady(t, vm, integrationVMReadyTimeout(vm, false))

		hostDir := t.TempDir()
		writeVirtioFSTestTree(t, hostDir)
		entry, _, err := addSharedFolderEntry(vm.dir, hostDir, "resumed-hot-apply", false)
		if err != nil {
			t.Fatalf("addSharedFolderEntry(): %v", err)
		}

		ctlDo(t, vm, &controlpb.ControlRequest{Type: "shared-folders-apply"})

		start := time.Now()
		_, err = mountSharedFoldersInGuest(vm.dir, defaultSharedFoldersMountPoint)
		elapsed := time.Since(start)
		if elapsed > 45*time.Second {
			t.Fatalf("mountSharedFoldersInGuest() took %s after resume, want bounded failure/success", elapsed)
		}

		if err == nil {
			assertVirtioFSTestTreeVisible(t, vm, filepath.Join(defaultSharedFoldersMountPoint, entry.Tag))
			return
		}

		errText := strings.ToLower(err.Error())
		if !strings.Contains(errText, "operation not permitted") &&
			!strings.Contains(errText, "mount shared folders") &&
			!strings.Contains(errText, "inspect mounted shared folders") {
			t.Fatalf("mountSharedFoldersInGuest() error = %v, want bounded VirtioFS failure", err)
		}

		ctlDo(t, vm, &controlpb.ControlRequest{Type: "ping"})
		ctlDo(t, vm, &controlpb.ControlRequest{Type: "agent-ping"})
	})

	t.Run("daemon-user-traversal-bounded", func(t *testing.T) {
		vm := cloneTestVM(t, baseVM)
		requireUserAgent(t, vm)

		hostDir := t.TempDir()
		writeVirtioFSTestTree(t, hostDir)
		entry, _, err := addSharedFolderEntry(vm.dir, hostDir, "daemon-user", false)
		if err != nil {
			t.Fatalf("addSharedFolderEntry(): %v", err)
		}

		ctlDo(t, vm, &controlpb.ControlRequest{Type: "shared-folders-apply"})
		if _, err := mountSharedFoldersInGuest(vm.dir, defaultSharedFoldersMountPoint); err != nil {
			t.Fatalf("mountSharedFoldersInGuest(): %v", err)
		}

		guestRoot := filepath.Join(defaultSharedFoldersMountPoint, entry.Tag)
		cmd := []string{"/bin/sh", "-lc", fmt.Sprintf("find %s -maxdepth 4 -print >/dev/null", shQuote(guestRoot))}

		daemon := agentExecResultTimeoutTB(t, vm, 20*time.Second, cmd...)
		user := userAgentExecResultTimeoutTB(t, vm, 20*time.Second, cmd...)
		if user.GetExitCode() != 0 {
			t.Fatalf("user traversal exit = %d\nstdout:\n%s\nstderr:\n%s", user.GetExitCode(), user.GetStdout(), user.GetStderr())
		}
		if daemon.GetExitCode() != 0 {
			t.Logf("daemon traversal exit = %d\nstdout:\n%s\nstderr:\n%s", daemon.GetExitCode(), daemon.GetStdout(), daemon.GetStderr())
		}
	})
}

func writeVirtioFSTestTree(t *testing.T, root string) {
	t.Helper()

	mustMkdirAll := func(path string) {
		t.Helper()
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("mkdir %q: %v", path, err)
		}
	}
	mustWriteFile := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatalf("write %q: %v", path, err)
		}
	}

	mustMkdirAll(filepath.Join(root, "src", "nested"))
	mustMkdirAll(filepath.Join(root, "docs"))
	mustWriteFile(filepath.Join(root, "README.md"), "virtiofs reliability fixture\n")
	mustWriteFile(filepath.Join(root, "src", "main.txt"), "hello from shared tree\n")
	mustWriteFile(filepath.Join(root, "src", "nested", "deep.txt"), "deep file\n")
	mustWriteFile(filepath.Join(root, "docs", "guide.txt"), "guide\n")
	if err := os.Symlink(filepath.Join("..", "src", "main.txt"), filepath.Join(root, "docs", "main-link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

func assertVirtioFSTestTreeVisible(t *testing.T, vm *testVM, guestRoot string) {
	t.Helper()

	agentExecExpectCodeTimeoutTB(t, vm, 20*time.Second, 0, "/bin/test", "-d", guestRoot)
	agentExecExpectCodeTimeoutTB(t, vm, 20*time.Second, 0, "/bin/test", "-f", filepath.Join(guestRoot, "README.md"))
	agentExecExpectCodeTimeoutTB(t, vm, 20*time.Second, 0, "/bin/test", "-f", filepath.Join(guestRoot, "src", "nested", "deep.txt"))
	assertVirtioFSBoundedList(t, vm, guestRoot)
	assertVirtioFSBoundedRead(t, vm, filepath.Join(guestRoot, "README.md"), "virtiofs reliability fixture")
	assertVirtioFSBoundedRead(t, vm, filepath.Join(guestRoot, "docs", "main-link.txt"), "hello from shared tree")
}

func assertVirtioFSBoundedList(t *testing.T, vm *testVM, guestRoot string) {
	t.Helper()

	result := agentExecResultTimeoutTB(t, vm, 20*time.Second, "/bin/ls", "-1", guestRoot)
	if result.GetExitCode() == 0 {
		got := strings.Fields(result.GetStdout())
		want := []string{"README.md", "docs", "src"}
		for _, name := range want {
			if !containsField(got, name) {
				t.Fatalf("ls %q missing %q\nstdout:\n%s\nstderr:\n%s", guestRoot, name, result.GetStdout(), result.GetStderr())
			}
		}
		return
	}
	assertVirtioFSBoundedPermissionError(t, "ls", result)
}

func assertVirtioFSBoundedRead(t *testing.T, vm *testVM, path, want string) {
	t.Helper()

	result := agentExecResultTimeoutTB(t, vm, 20*time.Second, "/bin/cat", path)
	if result.GetExitCode() == 0 {
		if strings.TrimSpace(result.GetStdout()) != want {
			t.Fatalf("cat %q = %q, want %q", path, strings.TrimSpace(result.GetStdout()), want)
		}
		return
	}
	assertVirtioFSBoundedPermissionError(t, "cat", result)
}

func assertVirtioFSBoundedPermissionError(t *testing.T, op string, result *controlpb.AgentExecResponse) {
	t.Helper()

	text := strings.ToLower(result.GetStderr() + "\n" + result.GetStdout())
	if !strings.Contains(text, "operation not permitted") {
		t.Fatalf("%s exit = %d\nstdout:\n%s\nstderr:\n%s", op, result.GetExitCode(), result.GetStdout(), result.GetStderr())
	}
}

func containsField(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
