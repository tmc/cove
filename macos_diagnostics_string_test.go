package main

import (
	"strings"
	"testing"
)

func TestVMStartDiagnosticsString(t *testing.T) {
	tests := []struct {
		name string
		in   vmStartDiagnostics
		want []string // substrings that must appear
	}{
		{
			name: "empty disk and dir leave unknown attached and unknown socket",
			in:   vmStartDiagnostics{QueueHandle: 0xdead, BootMode: "macos"},
			want: []string{"queue=0xdead", "boot_mode=macos", "disk_attached=unknown", "control_socket=unknown"},
		},
		{
			name: "missing disk path under nonexistent vm dir reports disk_attached=no",
			in: vmStartDiagnostics{
				VMDir:    t.TempDir(),
				BootMode: "linux",
				Recovery: true,
				Headless: true,
			},
			want: []string{"boot_mode=linux", "recovery=true", "headless=true", "disk_attached=no", "control_socket=not connected"},
		},
		{
			name: "explicit DiskPath wins over VMDir-derived path",
			in: vmStartDiagnostics{
				DiskPath: "/nonexistent/path/disk.img",
				BootMode: "recovery",
			},
			want: []string{"boot_mode=recovery", "disk_attached=no"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.String()
			for _, sub := range tt.want {
				if !strings.Contains(got, sub) {
					t.Errorf("String() = %q\n  missing: %q", got, sub)
				}
			}
		})
	}
}
