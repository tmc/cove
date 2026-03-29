package main

import (
	"strings"
	"testing"
)

func TestGenerateEmbeddedProvisionScript(t *testing.T) {
	config := ProvisionConfig{
		Username:          "testuser",
		Password:          "p@ss'word",
		Fullname:          "Test User",
		Admin:             true,
		BootstrapRecovery: true,
		InstallXcodeCLI:   false,
		EnableSSHD:        true,
	}

	result := generateEmbeddedProvisionScript(config)

	checks := []struct {
		name string
		want string
	}{
		{"username", "USERNAME='testuser'"},
		{"password", "PASSWORD='p@ss'\\''word'"},
		{"fullname", "FULLNAME='Test User'"},
		{"admin", `ADMIN="true"`},
		{"bootstrap", `BOOTSTRAP_RECOVERY="true"`},
		{"xcode", `INSTALL_XCODE_CLI="false"`},
		{"sshd", `ENABLE_SSHD="true"`},
		{"shebang", "#!/bin/bash"},
		{"date_format", "date '+%Y-%m-%d %H:%M:%S'"},
		{"marker", `MARKER="/var/db/.vz-provisioned"`},
		{"sysadminctl", "sysadminctl -addUser"},
		{"self_cleanup", "rm -f /Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist"},
	}

	for _, c := range checks {
		if !strings.Contains(result, c.want) {
			t.Errorf("%s: expected %q not found in output", c.name, c.want)
		}
	}
}

func TestGenerateEmbeddedProvisionScriptFullnameDefault(t *testing.T) {
	config := ProvisionConfig{
		Username: "alice",
		Password: "secret",
	}
	result := generateEmbeddedProvisionScript(config)
	if !strings.Contains(result, "FULLNAME='alice'") {
		t.Error("expected fullname to default to username")
	}
}

func TestGenerateEmbeddedLaunchDaemonPlist(t *testing.T) {
	plist := generateEmbeddedLaunchDaemonPlist()
	checks := []string{
		"com.github.tmc.vz-macos.provision",
		"/var/db/vz-provision.sh",
		"RunAtLoad",
		"LaunchOnlyOnce",
	}
	for _, want := range checks {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q", want)
		}
	}
}
