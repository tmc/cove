package main

import (
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
	if err := handleSecurityCommand([]string{"status"}, &out); err != nil {
		t.Fatalf("handleSecurityCommand: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"sandbox: host-containment",
		"host containment: true",
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

	var out strings.Builder
	if err := handleSecurityCommand([]string{"status", "-json"}, &out); err != nil {
		t.Fatalf("handleSecurityCommand: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"sandbox_level": "host-containment"`) || !strings.Contains(got, `"host_containment": true`) {
		t.Fatalf("security json = %s", got)
	}
}
