package main

import (
	"strings"
	"testing"
	"time"
)

func TestVMSelectionHelpers(t *testing.T) {
	tests := []struct {
		name         string
		selection    vmSelection
		wantLabel    string
		wantHintFlag string
		wantSocket   string
		wantDisk     string
		wantLinux    string
		wantStaging  string
		wantMarker   string
	}{
		{
			name:         "default vm",
			selection:    vmSelection{Directory: "/tmp/vms/default"},
			wantLabel:    "default",
			wantHintFlag: "",
			wantSocket:   "/tmp/vms/default/control.sock",
			wantDisk:     "/tmp/vms/default/disk.img",
			wantLinux:    "/tmp/vms/default/linux-disk.img",
			wantStaging:  "/tmp/vms/default/.provision",
			wantMarker:   "/tmp/vms/default/.inject-succeeded",
		},
		{
			name:         "named vm",
			selection:    vmSelection{Directory: "/tmp/vms/work", Name: "work"},
			wantLabel:    "work",
			wantHintFlag: " -vm work",
			wantSocket:   "/tmp/vms/work/control.sock",
			wantDisk:     "/tmp/vms/work/disk.img",
			wantLinux:    "/tmp/vms/work/linux-disk.img",
			wantStaging:  "/tmp/vms/work/.provision",
			wantMarker:   "/tmp/vms/work/.inject-succeeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.selection.elevationLabel(); got != tt.wantLabel {
				t.Fatalf("elevationLabel() = %q, want %q", got, tt.wantLabel)
			}
			if got := tt.selection.hintFlag(); got != tt.wantHintFlag {
				t.Fatalf("hintFlag() = %q, want %q", got, tt.wantHintFlag)
			}
			if got := tt.selection.controlSocketPath(); got != tt.wantSocket {
				t.Fatalf("controlSocketPath() = %q, want %q", got, tt.wantSocket)
			}
			if got := tt.selection.diskPath(); got != tt.wantDisk {
				t.Fatalf("diskPath() = %q, want %q", got, tt.wantDisk)
			}
			if got := tt.selection.linuxDiskPath(); got != tt.wantLinux {
				t.Fatalf("linuxDiskPath() = %q, want %q", got, tt.wantLinux)
			}
			if got := tt.selection.provisionStagingDir(); got != tt.wantStaging {
				t.Fatalf("provisionStagingDir() = %q, want %q", got, tt.wantStaging)
			}
			if got := tt.selection.injectSucceededMarker(); got != tt.wantMarker {
				t.Fatalf("injectSucceededMarker() = %q, want %q", got, tt.wantMarker)
			}
		})
	}
}

func TestGuestAgentUpgradeInstallScriptUsesRename(t *testing.T) {
	script := guestAgentUpgradeInstallScript("/tmp/vz-agent-upgrade", "/usr/local/bin/vz-agent")
	for _, want := range []string{
		"tmp='/tmp/vz-agent-upgrade'",
		"dest='/usr/local/bin/vz-agent'",
		"mv -f \"$tmp\" \"$dest\"",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "cp ") {
		t.Fatalf("script should not copy over the running executable:\n%s", script)
	}
}

func TestAgentUpgradeReconnectBudget(t *testing.T) {
	if agentUpgradeReconnectInitialDelay != 5*time.Second {
		t.Fatalf("agentUpgradeReconnectInitialDelay = %s, want 5s", agentUpgradeReconnectInitialDelay)
	}
	if agentUpgradeReconnectAttempts != 30 {
		t.Fatalf("agentUpgradeReconnectAttempts = %d, want 30", agentUpgradeReconnectAttempts)
	}
	if agentUpgradeReconnectDelay != 3*time.Second {
		t.Fatalf("agentUpgradeReconnectDelay = %s, want 3s", agentUpgradeReconnectDelay)
	}
	if agentUpgradeReconnectTimeout != 10*time.Second {
		t.Fatalf("agentUpgradeReconnectTimeout = %s, want 10s", agentUpgradeReconnectTimeout)
	}
	msg := agentUpgradeReconnectTimeoutMessage()
	for _, want := range []string{
		"agent installed and restart requested",
		"within 95s",
		"tried 30 reconnects",
		"retry cove ctl agent-ping or cove agent-upgrade",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("agentUpgradeReconnectTimeoutMessage() = %q, missing %q", msg, want)
		}
	}
}

func TestIsLinuxGuestOS(t *testing.T) {
	tests := []struct {
		name string
		os   string
		want bool
	}{
		{name: "ubuntu", os: "Ubuntu 24.04.3 LTS", want: true},
		{name: "linux", os: "Linux 6.17.0", want: true},
		{name: "debian", os: "Debian GNU/Linux 13", want: true},
		{name: "fedora", os: "Fedora Linux 42", want: true},
		{name: "macos", os: "macOS 15.4"},
		{name: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLinuxGuestOS(tt.os); got != tt.want {
				t.Fatalf("isLinuxGuestOS(%q) = %v, want %v", tt.os, got, tt.want)
			}
		})
	}
}

func TestAgentBuildTargetOS(t *testing.T) {
	tests := []struct {
		name string
		os   string
		want string
	}{
		{name: "ubuntu", os: "Ubuntu 24.04.3 LTS", want: "linux"},
		{name: "linux", os: "Linux", want: "linux"},
		{name: "macos", os: "macOS 15.4", want: "darwin"},
		{name: "unknown", want: "darwin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentBuildTargetOS(tt.os); got != tt.want {
				t.Fatalf("agentBuildTargetOS(%q) = %q, want %q", tt.os, got, tt.want)
			}
		})
	}
}
