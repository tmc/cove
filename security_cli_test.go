package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityStatusHostContainment(t *testing.T) {
	oldHostContainment := hostContainment
	oldSandbox := sandboxLevel
	oldNetwork := networkMode
	oldClipboard := enableClipboard
	oldAutoMount := autoMountVolumes
	oldAutoUpgrade := autoUpgradeAgent
	t.Cleanup(func() {
		hostContainment = oldHostContainment
		sandboxLevel = oldSandbox
		networkMode = oldNetwork
		enableClipboard = oldClipboard
		autoMountVolumes = oldAutoMount
		autoUpgradeAgent = oldAutoUpgrade
	})

	hostContainment = true
	sandboxLevel = ""
	networkMode = "nat"
	enableClipboard = true
	autoMountVolumes = true
	autoUpgradeAgent = true

	var out strings.Builder
	if err := handleSecurityCommand(commandEnv{Stdout: &out, Stderr: &bytes.Buffer{}}, []string{"status"}); err != nil {
		t.Fatalf("handleSecurityCommand: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"sandbox: host-containment",
		"host containment: true",
		"apple app sandbox: false",
		"network: none",
		"clipboard: false",
		"auto-mount volumes: false",
		"auto-upgrade agent: false",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("security status missing %q:\n%s", want, got)
		}
	}
}

func TestSecurityStatusAppleAppSandbox(t *testing.T) {
	t.Setenv(appleAppSandboxContainerEnv, "com.tmc.cove")
	t.Setenv("HOME", "/Users/tmc/Library/Containers/com.tmc.cove/Data")

	var out strings.Builder
	if err := handleSecurityCommand(commandEnv{Stdout: &out, Stderr: &bytes.Buffer{}}, []string{"status"}); err != nil {
		t.Fatalf("handleSecurityCommand: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"apple app sandbox: true",
		"apple app sandbox id: com.tmc.cove",
		"home: /Users/tmc/Library/Containers/com.tmc.cove/Data",
		"state root: /Users/tmc/Library/Containers/com.tmc.cove/Data/.vz",
		"vm root: /Users/tmc/Library/Containers/com.tmc.cove/Data/.vz/vms",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("security status missing %q:\n%s", want, got)
		}
	}
}

func TestSecurityStatusJSON(t *testing.T) {
	oldHostContainment := hostContainment
	oldSandbox := sandboxLevel
	oldNetwork := networkMode
	t.Cleanup(func() {
		hostContainment = oldHostContainment
		sandboxLevel = oldSandbox
		networkMode = oldNetwork
	})

	hostContainment = true
	sandboxLevel = ""
	networkMode = "none"
	t.Setenv(appleAppSandboxContainerEnv, "com.tmc.cove")

	var out strings.Builder
	if err := handleSecurityCommand(commandEnv{Stdout: &out, Stderr: &bytes.Buffer{}}, []string{"status", "-json"}); err != nil {
		t.Fatalf("handleSecurityCommand: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`"sandbox_level": "host-containment"`,
		`"host_containment": true`,
		`"apple_app_sandbox": true`,
		`"apple_app_sandbox_id": "com.tmc.cove"`,
		`"state_root":`,
		`"vm_root":`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("security json missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"apple_app_sandbox_id": ""`) {
		t.Fatalf("security json = %s", got)
	}
}

func TestSecuritySandboxProbeUnixSocket(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cove-security-probe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	check := probeSandboxUnixSocket(filepath.Join(home, ".vz", "vms"))
	if check.Status != "pass" {
		t.Fatalf("probeSandboxUnixSocket status = %q message = %q", check.Status, check.Message)
	}
	if !strings.Contains(check.Path, filepath.Join(home, ".vz", "vms")) {
		t.Fatalf("probeSandboxUnixSocket path = %q, want under test home", check.Path)
	}
}

func TestSecuritySandboxProbeLoopbackTCP(t *testing.T) {
	check := probeSandboxLoopbackTCP()
	if check.Status != "pass" {
		t.Fatalf("probeSandboxLoopbackTCP status = %q message = %q", check.Status, check.Message)
	}
	if !strings.HasPrefix(check.Path, "127.0.0.1:") {
		t.Fatalf("probeSandboxLoopbackTCP path = %q, want loopback address", check.Path)
	}
}

func TestSecuritySandboxProbeHelperUnavailable(t *testing.T) {
	oldDial := probeSandboxDialHelper
	t.Cleanup(func() { probeSandboxDialHelper = oldDial })
	probeSandboxDialHelper = func() (net.Conn, error) {
		return nil, errHelperUnavailable
	}

	check := probeSandboxHelperIPC()
	if check.Status != "skip" {
		t.Fatalf("probeSandboxHelperIPC status = %q, want skip", check.Status)
	}
	if !strings.Contains(check.Message, "helper socket not present") {
		t.Fatalf("probeSandboxHelperIPC message = %q", check.Message)
	}
}

func TestSecuritySandboxProbeHelperPing(t *testing.T) {
	oldDial := probeSandboxDialHelper
	t.Cleanup(func() { probeSandboxDialHelper = oldDial })
	probeSandboxDialHelper = func() (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			var req helperRequest
			if err := json.NewDecoder(server).Decode(&req); err != nil {
				return
			}
			if req.Op != "ping" {
				_ = json.NewEncoder(server).Encode(helperResponse{Error: "unexpected op"})
				return
			}
			_ = json.NewEncoder(server).Encode(helperResponse{OK: true})
		}()
		return client, nil
	}

	check := probeSandboxHelperIPC()
	if check.Status != "pass" {
		t.Fatalf("probeSandboxHelperIPC status = %q message = %q, want pass", check.Status, check.Message)
	}
}

func TestRunSecurityCommandUsesEnvStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := commandEnv{Stdout: &stdout, Stderr: &stderr}
	if code := runSecurityCommand(env, "security", []string{"-h"}); code != 0 {
		t.Fatalf("runSecurityCommand help exit = %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("help wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Usage: cove security") {
		t.Fatalf("help stderr missing usage: %q", stderr.String())
	}
}

func TestRunSecurityCommandBadFlagUsesEnvStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := commandEnv{Stdout: &stdout, Stderr: &stderr}
	if code := runSecurityCommand(env, "security", []string{"-bogus"}); code == 0 {
		t.Fatal("runSecurityCommand bad flag exit = 0")
	}
	if stdout.Len() != 0 {
		t.Fatalf("bad flag wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("bad flag stderr missing flag error: %q", stderr.String())
	}
}
