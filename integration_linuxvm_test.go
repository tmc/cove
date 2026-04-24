//go:build integration && darwin && arm64

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentstate "github.com/tmc/vz-macos/internal/agent"
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
		waitVMReady(t, vm, integrationVMReadyTimeout(vm, false))
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

	t.Run("proxy-linux", func(t *testing.T) {
		cloneName := integrationCloneName(t.Name())
		if err := CloneVM(CloneOptions{Source: vm.name, Target: cloneName, Linked: true}); err != nil {
			t.Fatalf("CloneVM() error = %v", err)
		}
		clone := clonedTestVM(t, cloneName, true)

		startTestVMWithArgs(t, clone, "-proxy", "http://192.168.64.1:8080", "-no-agent")
		waitVMReadyTB(t, clone, integrationVMReadyTimeout(clone, false))
		waitForLinuxProxyFiles(t, clone, true)

		clone.cleanupTB(t)
		startTestVM(t, clone)
		waitVMReadyTB(t, clone, integrationVMReadyTimeout(clone, false))
		waitForLinuxProxyFiles(t, clone, false)
	})

	t.Run("proxy-preflight-no-agent", func(t *testing.T) {
		cloneName := integrationCloneName(t.Name())
		if err := CloneVM(CloneOptions{Source: vm.name, Target: cloneName, Linked: true}); err != nil {
			t.Fatalf("CloneVM() error = %v", err)
		}
		clone := clonedTestVM(t, cloneName, true)

		cfg, err := LoadVMConfig(clone.dir)
		if err != nil {
			t.Fatalf("LoadVMConfig() error = %v", err)
		}
		cfg.Agent = &VMAgentConfig{
			Platform: agentstate.PlatformLinux,
			Source:   agentstate.SourceInstall,
		}
		if err := SaveVMConfig(clone.dir, cfg); err != nil {
			t.Fatalf("SaveVMConfig() error = %v", err)
		}

		bin := buildIntegrationBinary(t)
		cmd := exec.Command(bin, "-vm", clone.name, "-linux", "-proxy", "http://192.168.64.1:8080", "run")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("run without agent unexpectedly succeeded:\n%s", out)
		}
		if !strings.Contains(strings.ToLower(string(out)), "provision-agent") {
			t.Fatalf("run without agent output = %q, want remediation mentioning provision-agent", out)
		}
	})
}

func waitForLinuxProxyFiles(t *testing.T, vm *testVM, present bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	envPath := filepath.Join("/etc", "environment.d", proxyEnvFileName)
	profilePath := filepath.Join("/etc", "profile.d", proxyProfileFileName)
	for time.Now().Before(deadline) {
		envOK := linuxProxyFileState(t, vm, envPath)
		profileOK := linuxProxyFileState(t, vm, profilePath)
		if envOK == present && profileOK == present {
			if present {
				env := string(agentRead(t, vm, envPath))
				if !strings.Contains(env, "HTTP_PROXY=http://192.168.64.1:8080") {
					t.Fatalf("proxy env file missing configured proxy:\n%s", env)
				}
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("proxy file state did not converge: want present=%v", present)
}

func linuxProxyFileState(t *testing.T, vm *testVM, path string) bool {
	t.Helper()
	result := agentExecResult(t, vm, "/bin/sh", "-lc", "test -f "+shellQuote(path))
	return result.GetExitCode() == 0
}
