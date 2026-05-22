package main

import (
	"strings"
	"testing"

	"github.com/tmc/cove/internal/password"
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

	result, err := generateEmbeddedProvisionScript(config)
	if err != nil {
		t.Fatalf("generateEmbeddedProvisionScript: %v", err)
	}

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
		{"autologin_kickstart", "launchctl kickstart -k system/com.github.tmc.vz-macos.autologin"},
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
	result, err := generateEmbeddedProvisionScript(config)
	if err != nil {
		t.Fatalf("generateEmbeddedProvisionScript: %v", err)
	}
	if !strings.Contains(result, "FULLNAME='alice'") {
		t.Error("expected fullname to default to username")
	}
}

func TestKCPasswordDoesNotContainPlaintext(t *testing.T) {
	passwords := []string{"secret123", "p@ssw0rd!", "hunter2"}
	for _, p := range passwords {
		encoded := password.EncodeKC(p)
		if strings.Contains(string(encoded), p) {
			t.Errorf("kcpassword for %q contains plaintext", p)
		}
	}
}

func TestProvisionScriptContainsPasswordOnlyInVariable(t *testing.T) {
	config := ProvisionConfig{
		Username: "testuser",
		Password: "MyS3cretP@ss!",
	}
	script, err := generateEmbeddedProvisionScript(config)
	if err != nil {
		t.Fatalf("generateEmbeddedProvisionScript: %v", err)
	}

	// The password should appear only as PASSWORD='...' variable assignment.
	// Count occurrences of the password string.
	count := strings.Count(script, "MyS3cretP@ss!")
	if count == 0 {
		t.Fatal("password not found in provision script at all")
	}
	if count != 1 {
		t.Errorf("password appears %d times in provision script, want 1 (only in PASSWORD= assignment)", count)
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

func TestGenerateEmbeddedAutoLoginScript(t *testing.T) {
	script, err := generateEmbeddedAutoLoginScript("testuser")
	if err != nil {
		t.Fatalf("generateEmbeddedAutoLoginScript: %v", err)
	}
	checks := []string{
		"USERNAME='testuser'",
		"autoLoginUserScreenLocked",
		"killall loginwindow",
		"Console user is $USERNAME",
	}
	for _, want := range checks {
		if !strings.Contains(script, want) {
			t.Errorf("autologin script missing %q", want)
		}
	}
}
