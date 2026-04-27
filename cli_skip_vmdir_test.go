package main

import "testing"

// TestSubcommandSkipsVMDir guards the allowlist of subcommands that must boot
// without a writable ~/.vz/vms. Adding a command here means weighing whether
// it really has zero need for VM-dir state — be conservative.
func TestSubcommandSkipsVMDir(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"helper bare", []string{"helper"}, true},
		{"helper daemon", []string{"helper", "daemon"}, true},
		{"helper status", []string{"helper", "status"}, true},
		{"version", []string{"version"}, true},
		{"vm tree", []string{"vm", "tree"}, true},
		{"vm tree extra args still skips startup VM dir", []string{"vm", "tree", "extra"}, true},
		{"run is not allowlisted", []string{"run"}, false},
		{"install is not allowlisted", []string{"install"}, false},
		{"vm is not allowlisted", []string{"vm", "list"}, false},
		{"unknown is not allowlisted", []string{"banana"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subcommandSkipsVMDir(tt.args); got != tt.want {
				t.Errorf("subcommandSkipsVMDir(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
