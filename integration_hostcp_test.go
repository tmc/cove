//go:build integration && darwin && arm64

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func testHostCp(t *testing.T, vm *testVM) {
	t.Run("dir-copy", func(t *testing.T) {
		// Create a small test directory on the host.
		hostDir := t.TempDir()
		testDir := filepath.Join(hostDir, "test-app.app")
		if err := os.MkdirAll(filepath.Join(testDir, "Contents"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(testDir, "Contents", "Info.plist"), []byte("<plist/>"), 0644); err != nil {
			t.Fatal(err)
		}

		guestPath := "/tmp/vz-integration-hostcp-test-app.app"
		t.Cleanup(func() { cleanupGuestPaths(t, vm, guestPath) })

		// First copy should succeed.
		resp := agentCopyDirToGuest(t, vm, testDir, guestPath, false)
		if strings.Contains(resp, "already exists") {
			t.Fatalf("first copy should not report already exists: %s", resp)
		}

		// Verify files arrived.
		got := agentExec(t, vm, "/bin/cat", guestPath+"/Contents/Info.plist")
		if !strings.Contains(got, "<plist/>") {
			t.Fatalf("expected plist content, got %q", got)
		}
	})

	t.Run("skip-if-exists", func(t *testing.T) {
		// Create a small test directory on the host.
		hostDir := t.TempDir()
		testDir := filepath.Join(hostDir, "skip-test.app")
		if err := os.MkdirAll(filepath.Join(testDir, "Contents"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(testDir, "Contents", "marker"), []byte("v1"), 0644); err != nil {
			t.Fatal(err)
		}

		guestPath := "/tmp/vz-integration-hostcp-skip.app"
		t.Cleanup(func() { cleanupGuestPaths(t, vm, guestPath) })

		// First copy.
		agentCopyDirToGuest(t, vm, testDir, guestPath, false)

		// Second copy should skip (already exists).
		resp := agentCopyDirToGuest(t, vm, testDir, guestPath, false)
		if !strings.Contains(resp, "already exists") {
			t.Fatalf("second copy should report already exists, got: %s", resp)
		}
	})

	t.Run("force-overwrite", func(t *testing.T) {
		// Create a small test directory on the host.
		hostDir := t.TempDir()
		testDir := filepath.Join(hostDir, "force-test.app")
		if err := os.MkdirAll(filepath.Join(testDir, "Contents"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(testDir, "Contents", "marker"), []byte("v1"), 0644); err != nil {
			t.Fatal(err)
		}

		guestPath := "/tmp/vz-integration-hostcp-force.app"
		t.Cleanup(func() { cleanupGuestPaths(t, vm, guestPath) })

		// First copy.
		agentCopyDirToGuest(t, vm, testDir, guestPath, false)

		// Update content on host.
		if err := os.WriteFile(filepath.Join(testDir, "Contents", "marker"), []byte("v2"), 0644); err != nil {
			t.Fatal(err)
		}

		// Force copy should re-copy.
		resp := agentCopyDirToGuest(t, vm, testDir, guestPath, true)
		if strings.Contains(resp, "already exists") {
			t.Fatalf("force copy should not skip: %s", resp)
		}

		// Verify updated content.
		got := agentExec(t, vm, "/bin/cat", guestPath+"/Contents/marker")
		if strings.TrimSpace(got) != "v2" {
			t.Fatalf("expected v2 after force copy, got %q", got)
		}
	})

	t.Run("temp-tar-cleanup", func(t *testing.T) {
		// Create a small test directory.
		hostDir := t.TempDir()
		testDir := filepath.Join(hostDir, "cleanup-test.app")
		if err := os.MkdirAll(filepath.Join(testDir, "Contents"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(testDir, "Contents", "data"), []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}

		guestPath := "/tmp/vz-integration-hostcp-cleanup.app"
		t.Cleanup(func() { cleanupGuestPaths(t, vm, guestPath) })

		// Copy should succeed.
		agentCopyDirToGuest(t, vm, testDir, guestPath, true)

		// Verify the temp tar file was cleaned up.
		tmpTar := "/tmp/vz-cp-" + filepath.Base(testDir) + ".tar"
		result := agentExecResult(t, vm, "/bin/test", "-f", tmpTar)
		if result.GetExitCode() == 0 {
			t.Fatalf("temp tar %q should have been cleaned up", tmpTar)
		}
	})

	t.Run("vzscript-host-cp", func(t *testing.T) {
		// Create a test directory to copy via vzscript host-cp command.
		hostDir := t.TempDir()
		testDir := filepath.Join(hostDir, "vzscript-cp.app")
		if err := os.MkdirAll(filepath.Join(testDir, "Contents"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(testDir, "Contents", "hello"), []byte("world"), 0644); err != nil {
			t.Fatal(err)
		}

		guestPath := "/tmp/vz-integration-vzscript-hostcp.app"
		t.Cleanup(func() { cleanupGuestPaths(t, vm, guestPath) })

		// Run via vzscript engine.
		script := strings.ReplaceAll(`# host-cp integration test
host-cp HOST_DIR GUEST_PATH
`, "HOST_DIR", testDir)
		script = strings.ReplaceAll(script, "GUEST_PATH", guestPath)

		scriptPath := writeTempVZScript(t, script)
		runVZScriptFile(t, vm, scriptPath)

		// Verify content.
		got := agentExec(t, vm, "/bin/cat", guestPath+"/Contents/hello")
		if strings.TrimSpace(got) != "world" {
			t.Fatalf("vzscript host-cp: expected 'world', got %q", got)
		}

		// Run again — should skip.
		runVZScriptFile(t, vm, scriptPath)

		// Run with -force.
		forceScript := strings.ReplaceAll(`# host-cp force test
host-cp -force HOST_DIR GUEST_PATH
`, "HOST_DIR", testDir)
		forceScript = strings.ReplaceAll(forceScript, "GUEST_PATH", guestPath)
		forceScriptPath := writeTempVZScript(t, forceScript)
		runVZScriptFile(t, vm, forceScriptPath)
	})
}

// agentCopyDirToGuest copies a host directory to the guest and returns the
// response data string. If overwrite is true, sets the Overwrite flag to
// force re-copy even if the destination already exists.
func agentCopyDirToGuest(t *testing.T, vm *testVM, hostPath, guestPath string, overwrite bool) string {
	t.Helper()

	req := &controlpb.ControlRequest{
		Type:      "agent-cp",
		AuthToken: vm.token,
		Command: &controlpb.ControlRequest_AgentCp{
			AgentCp: &controlpb.AgentCopyCommand{
				HostPath:  hostPath,
				GuestPath: guestPath,
				ToGuest:   true,
				Overwrite: overwrite,
			},
		},
	}
	resp, err := ctlSendRequest(vm.sock, req, 5*time.Minute, req.Type)
	if err != nil {
		t.Fatalf("agent-cp dir %s -> %s: %v", hostPath, guestPath, err)
	}
	if !resp.Success {
		t.Fatalf("agent-cp dir %s -> %s: %s", hostPath, guestPath, resp.Error)
	}
	return resp.Data
}
