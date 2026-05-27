package main

import (
	"bytes"
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

	var out strings.Builder
	if err := handleSecurityCommand(commandEnv{Stdout: &out, Stderr: &bytes.Buffer{}}, []string{"status"}); err != nil {
		t.Fatalf("handleSecurityCommand: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"apple app sandbox: true",
		"apple app sandbox id: com.tmc.cove",
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
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("security json missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"apple_app_sandbox_id": ""`) {
		t.Fatalf("security json = %s", got)
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
