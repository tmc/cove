package main

import "testing"

func TestDefaultUserDataPath(t *testing.T) {
	got := DefaultUserDataPath("/tmp/vm")
	want := "/tmp/vm/userdata.sparsebundle"
	if got != want {
		t.Errorf("DefaultUserDataPath(%q) = %q, want %q", "/tmp/vm", got, want)
	}
}

func TestParseMountStrategy(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    MountStrategy
		wantErr bool
	}{
		{"empty default", "", MountStrategyVolumes, false},
		{"volumes", "volumes", MountStrategyVolumes, false},
		{"volumes uppercase", "VOLUMES", MountStrategyVolumes, false},
		{"symlinks", "symlinks", MountStrategySymlinks, false},
		{"direct", "direct", MountStrategyDirect, false},
		{"invalid", "invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMountStrategy(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMountStrategy(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseMountStrategy(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
