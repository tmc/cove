package main

import "testing"

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
