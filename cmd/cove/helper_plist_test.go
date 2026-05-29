package main

import (
	"strings"
	"testing"
)

// TestHelperLaunchdPlistShape verifies the LaunchDaemon plist body emitted
// by helperLaunchdPlist has the v0.1.1 hardening: ThrottleInterval and
// SuccessfulExit-conditional KeepAlive. Regression for v0.1.0 unconditional
// KeepAlive=true that crash-looped at ~6/min when the installed binary was
// stale (see project_cove_helper_crashloop.md).
func TestHelperLaunchdPlistShape(t *testing.T) {
	got := helperLaunchdPlist("com.tmc.cove.helper", "/usr/local/libexec/cove-helper")

	// Sanity: required identity fields.
	for _, want := range []string{
		"<string>com.tmc.cove.helper</string>",
		"<string>/usr/local/libexec/cove-helper</string>",
		"<string>helper</string>",
		"<string>daemon</string>",
		"<key>RunAtLoad</key>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plist missing required field %q; got:\n%s", want, got)
		}
	}

	// Hardening: ThrottleInterval present (caps respawn rate).
	if !strings.Contains(got, "<key>ThrottleInterval</key>") {
		t.Errorf("plist missing ThrottleInterval; got:\n%s", got)
	}
	if !strings.Contains(got, "<integer>30</integer>") {
		t.Errorf("ThrottleInterval should be 30; got:\n%s", got)
	}

	// Hardening: KeepAlive must be a SuccessfulExit=false dict, not <true/>.
	// A clean exit 0 should not trigger respawn.
	if strings.Contains(got, "<key>KeepAlive</key>\n  <true/>") {
		t.Errorf("KeepAlive must not be unconditional <true/>; got:\n%s", got)
	}
	if !strings.Contains(got, "<key>KeepAlive</key>") {
		t.Errorf("plist missing KeepAlive key; got:\n%s", got)
	}
	if !strings.Contains(got, "<key>SuccessfulExit</key>") {
		t.Errorf("KeepAlive must be conditional on SuccessfulExit; got:\n%s", got)
	}

	// Order: KeepAlive's SuccessfulExit must appear within the KeepAlive dict
	// — not as a sibling top-level key.
	keepAliveIdx := strings.Index(got, "<key>KeepAlive</key>")
	successfulExitIdx := strings.Index(got, "<key>SuccessfulExit</key>")
	if keepAliveIdx < 0 || successfulExitIdx < 0 || successfulExitIdx < keepAliveIdx {
		t.Errorf("SuccessfulExit must follow KeepAlive in plist; got:\n%s", got)
	}

	// Hardening: EnvironmentVariables must set HOME so os.UserHomeDir()
	// resolves and ~/.vz path resolution does not fall back to mkdir .vz
	// against cwd / (EROFS). Regression for project_cove_helper_crashloop.
	if !strings.Contains(got, "<key>EnvironmentVariables</key>") {
		t.Errorf("plist missing EnvironmentVariables; got:\n%s", got)
	}
	if !strings.Contains(got, "<key>HOME</key>") || !strings.Contains(got, "<string>/var/root</string>") {
		t.Errorf("plist must set HOME=/var/root in EnvironmentVariables; got:\n%s", got)
	}

	// Hardening: PATH must exclude /usr/local/bin. The helper runs as root and
	// invokes launchctl/diskutil/mount by bare name; /usr/local/bin is
	// admin-writable and would let a local admin hijack a root-priv exec.
	if !strings.Contains(got, "<string>/usr/sbin:/sbin:/usr/bin:/bin</string>") {
		t.Errorf("plist PATH must be /usr/sbin:/sbin:/usr/bin:/bin (no /usr/local/bin); got:\n%s", got)
	}
}

// TestHelperLaunchdPlistInterpolation verifies the label and binary path
// arguments are honored, so reinstalls with a moved binary work correctly.
func TestHelperLaunchdPlistInterpolation(t *testing.T) {
	got := helperLaunchdPlist("com.example.test", "/opt/test/cove-helper")
	if !strings.Contains(got, "<string>com.example.test</string>") {
		t.Errorf("label not interpolated; got:\n%s", got)
	}
	if !strings.Contains(got, "<string>/opt/test/cove-helper</string>") {
		t.Errorf("binary path not interpolated; got:\n%s", got)
	}
}
