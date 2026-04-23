package main

import "testing"

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
	}{
		{
			name:         "default vm",
			selection:    vmSelection{Directory: "/tmp/vms/default"},
			wantLabel:    "default",
			wantHintFlag: "",
			wantSocket:   "/tmp/vms/default/control.sock",
			wantDisk:     "/tmp/vms/default/disk.img",
			wantLinux:    "/tmp/vms/default/linux-disk.img",
		},
		{
			name:         "named vm",
			selection:    vmSelection{Directory: "/tmp/vms/work", Name: "work"},
			wantLabel:    "work",
			wantHintFlag: " -vm work",
			wantSocket:   "/tmp/vms/work/control.sock",
			wantDisk:     "/tmp/vms/work/disk.img",
			wantLinux:    "/tmp/vms/work/linux-disk.img",
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
		})
	}
}
