//go:build integration && darwin && arm64

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testAgent(t *testing.T, vm *testVM) {
	t.Run("ping", func(t *testing.T) {
		version := strings.TrimSpace(agentPingVersion(t, vm))
		if version == "" {
			t.Fatal("agent-ping returned empty version")
		}
	})

	t.Run("info", func(t *testing.T) {
		info := agentInfoResponse(t, vm)
		if strings.TrimSpace(info.GetHostname()) == "" {
			t.Fatal("agent-info returned empty hostname")
		}
		if strings.TrimSpace(info.GetOs()) == "" {
			t.Fatal("agent-info returned empty os")
		}
		if strings.TrimSpace(info.GetArch()) == "" {
			t.Fatal("agent-info returned empty arch")
		}
	})

	t.Run("exec", func(t *testing.T) {
		if got := agentExec(t, vm, "/bin/echo", "hello"); got != "hello\n" {
			t.Fatalf("agent-exec echo: got %q, want %q", got, "hello\n")
		}

		result := agentExecExpectCode(t, vm, 1, "/usr/bin/false")
		if result.GetStdout() != "" {
			t.Fatalf("agent-exec false: unexpected stdout %q", result.GetStdout())
		}

		env := agentExec(t, vm, "/usr/bin/env")
		if !strings.Contains(env, "PATH=") {
			t.Fatalf("agent-exec env: PATH missing from output:\n%s", env)
		}
	})

	t.Run("user-exec", func(t *testing.T) {
		requireGUI(t)

		result := userAgentExecExpectCode(t, vm, 0, "/usr/bin/id", "-un")
		user := strings.TrimSpace(result.GetStdout())
		if user == "" {
			t.Fatal("agent-user-exec id -un: empty stdout")
		}
		if user == "root" {
			t.Fatalf("agent-user-exec id -un: got %q, want non-root GUI user", user)
		}
	})

	t.Run("file-io", func(t *testing.T) {
		guestPath := "/tmp/vz-integration-agent-file.txt"
		t.Cleanup(func() { cleanupGuestPaths(t, vm, guestPath) })

		want := []byte("integration-agent-file-io\n")
		agentWrite(t, vm, guestPath, want, 0600)

		if got := agentRead(t, vm, guestPath); !bytes.Equal(got, want) {
			t.Fatalf("agent-read %q: got %q, want %q", guestPath, got, want)
		}

		mode := strings.TrimSpace(agentExec(t, vm, "/usr/bin/stat", "-f", "%Lp", guestPath))
		if mode != "600" {
			t.Fatalf("guest mode %q: got %q, want %q", guestPath, mode, "600")
		}
	})

	t.Run("copy", func(t *testing.T) {
		hostDir := t.TempDir()
		hostSrc := filepath.Join(hostDir, "source.txt")
		hostDst := filepath.Join(hostDir, "roundtrip.txt")
		guestPath := "/tmp/vz-integration-agent-copy.txt"
		t.Cleanup(func() { cleanupGuestPaths(t, vm, guestPath) })

		want := []byte("integration-agent-copy\n")
		if err := os.WriteFile(hostSrc, want, 0644); err != nil {
			t.Fatalf("write host source %q: %v", hostSrc, err)
		}

		agentCopyToGuest(t, vm, hostSrc, guestPath)
		if got := agentRead(t, vm, guestPath); !bytes.Equal(got, want) {
			t.Fatalf("guest copy %q: got %q, want %q", guestPath, got, want)
		}

		agentCopyFromGuest(t, vm, guestPath, hostDst)
		got, err := os.ReadFile(hostDst)
		if err != nil {
			t.Fatalf("read host copy %q: %v", hostDst, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("host roundtrip %q: got %q, want %q", hostDst, got, want)
		}
	})
}
