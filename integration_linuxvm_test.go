//go:build integration && darwin && arm64

package main

import (
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func testLinuxAgent(t *testing.T, vm *testVM) {
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
		os := strings.TrimSpace(info.GetOs())
		if os == "" {
			t.Fatal("agent-info returned empty os")
		}
		if !strings.Contains(strings.ToLower(os), "linux") {
			t.Fatalf("agent-info os: got %q, want linux", os)
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

	t.Run("uname", func(t *testing.T) {
		out := strings.TrimSpace(agentExec(t, vm, "/bin/uname", "-s"))
		if out != "Linux" {
			t.Fatalf("uname -s: got %q, want %q", out, "Linux")
		}
	})
}

func testLinuxCtl(t *testing.T, vm *testVM) {
	t.Run("status", func(t *testing.T) {
		status := statusResponse(t, vm)
		if got := canonicalVMState(status.GetState()); got != "running" {
			t.Fatalf("status state: got %q, want %q", got, "running")
		}
	})

	t.Run("pause-resume", func(t *testing.T) {
		status := statusResponse(t, vm)
		if !status.GetCanPause() {
			t.Skip("pause not supported for this VM")
		}

		ctlDo(t, vm, &controlpb.ControlRequest{Type: "pause"})
		waitVMState(t, vm, "paused", 30*time.Second)

		ctlDo(t, vm, &controlpb.ControlRequest{Type: "resume"})
		waitVMState(t, vm, "running", 30*time.Second)
		waitVMReady(t, vm, 2*time.Minute)
	})
}

func testLinuxNetwork(t *testing.T, vm *testVM) {
	t.Run("guest-curl", func(t *testing.T) {
		// Linux uses /usr/bin/curl or might need wget; try curl first.
		out := strings.TrimSpace(agentExec(t, vm,
			"/usr/bin/curl",
			"-fsSL",
			"-o", "/dev/null",
			"-w", "%{http_code}",
			"https://example.com",
		))
		if out != "200" {
			t.Fatalf("guest curl: got %q, want %q", out, "200")
		}
	})
}
