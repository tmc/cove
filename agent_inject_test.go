package main

import "testing"

func TestAgentVersionCompare(t *testing.T) {
	tests := []struct {
		name  string
		host  string
		guest string
		want  versionRelation
	}{
		{"identical release", "v0.2.3", "v0.2.3", versionEqual},
		{"identical commit", "abc12345", "abc12345", versionEqual},
		{"guest older patch", "v0.2.3", "v0.2.2", versionGuestOlder},
		{"guest older minor", "v0.3.0", "v0.2.9", versionGuestOlder},
		{"guest older major", "v1.0.0", "v0.9.9", versionGuestOlder},
		{"guest newer patch", "v0.2.2", "v0.2.3", versionGuestNewer},
		{"guest newer minor", "v0.2.3", "v0.3.0", versionGuestNewer},
		{"guest newer major", "v0.9.9", "v1.0.0", versionGuestNewer},
		{"semver without v prefix", "0.2.2", "0.2.3", versionGuestNewer},
		{"semver with prerelease equal majmin", "v0.2.3-rc1", "v0.2.3-rc2", versionEqual},
		{"two distinct commits (not semver)", "abc12345", "def67890", versionDifferent},
		{"semver vs commit", "v0.2.3", "abc12345", versionDifferent},
		{"host empty", "", "v0.2.3", versionUnknown},
		{"guest empty", "v0.2.3", "", versionUnknown},
		{"both empty", "", "", versionUnknown},
		{"host dev", "dev", "v0.2.3", versionUnknown},
		{"guest dev", "v0.2.3", "dev", versionUnknown},
		{"both dev", "dev", "dev", versionUnknown},
		{"host unknown", "unknown", "v0.2.3", versionUnknown},
		{"guest unknown", "v0.2.3", "unknown", versionUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentVersionCompare(tt.host, tt.guest); got != tt.want {
				t.Errorf("agentVersionCompare(%q, %q) = %d, want %d", tt.host, tt.guest, got, tt.want)
			}
		})
	}
}

func TestAgentVersionsEqual(t *testing.T) {
	tests := []struct {
		name  string
		host  string
		guest string
		want  bool
	}{
		{"identical release", "v0.2.3", "v0.2.3", true},
		{"identical commit", "abc12345", "abc12345", true},
		{"mismatch release", "v0.2.3", "v0.2.4", false},
		{"mismatch commit", "abc12345", "def67890", false},
		{"host empty", "", "v0.2.3", false},
		{"guest empty", "v0.2.3", "", false},
		{"both empty", "", "", false},
		{"host dev", "dev", "v0.2.3", false},
		{"guest dev", "v0.2.3", "dev", false},
		{"both dev", "dev", "dev", false},
		{"host unknown", "unknown", "v0.2.3", false},
		{"guest unknown", "v0.2.3", "unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentVersionsEqual(tt.host, tt.guest); got != tt.want {
				t.Errorf("agentVersionsEqual(%q, %q) = %v, want %v", tt.host, tt.guest, got, tt.want)
			}
		})
	}
}

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
