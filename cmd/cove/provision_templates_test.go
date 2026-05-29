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
		{"self_cleanup", "rm -f /Library/LaunchDaemons/com.tmc.cove.provision.plist"},
		{"autologin_kickstart", "launchctl kickstart -k system/com.tmc.cove.autologin"},
		{"reboot", "shutdown -r now"},
	}

	for _, c := range checks {
		if !strings.Contains(result, c.want) {
			t.Errorf("%s: expected %q not found in output", c.name, c.want)
		}
	}
}

// TestProvisionScriptRebootsOnlyWhenAutoLoginReady verifies the macOS 26
// reboot-after-provision fix is guarded: the reboot must be gated on
// AUTOLOGIN_READY, prerequisites (user, kcpassword, loginwindow autoLoginUser)
// must be checked, the marker is flushed with sync, and the legacy
// killall/kickstart recovery remains as a fallback when guards fail.
func TestProvisionScriptRebootsOnlyWhenAutoLoginReady(t *testing.T) {
	script, err := generateEmbeddedProvisionScript(ProvisionConfig{
		Username: "testuser",
		Password: "secret123",
		Admin:    true,
	})
	if err != nil {
		t.Fatalf("generateEmbeddedProvisionScript: %v", err)
	}

	mustContain := []string{
		"AUTOLOGIN_READY",                       // guard variable
		"/etc/kcpassword",                       // kcpassword presence check
		"autoLoginUser",                         // loginwindow value check
		`if [ "$AUTOLOGIN_READY" = "true" ]`,    // reboot is gated on the guard
		"vz-provision: user verified, rebooting for clean auto-login", // log line before reboot
		"sync",            // flush before reboot
		"shutdown -r now", // the reboot itself
		"killall loginwindow",                                  // legacy fallback retained
		"launchctl kickstart -k system/com.tmc.cove.autologin", // legacy fallback retained
	}
	for _, s := range mustContain {
		if !strings.Contains(script, s) {
			t.Errorf("provision script missing %q", s)
		}
	}

	// The reboot must come before the legacy fallback in source order, and the
	// fallback's killall must not be the only path (i.e. reboot is preferred).
	rebootIdx := strings.Index(script, "shutdown -r now")
	fallbackIdx := strings.LastIndex(script, "killall loginwindow")
	if rebootIdx < 0 || fallbackIdx < 0 || rebootIdx > fallbackIdx {
		t.Errorf("reboot (idx %d) should appear before legacy fallback killall (idx %d)", rebootIdx, fallbackIdx)
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
		"com.tmc.cove.provision",
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
