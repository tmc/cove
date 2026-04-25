package main

import (
	"reflect"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// TestAgentMountVolumesDispatchPerGuestOS asserts the mount-args contract
// that handleAgentMountVolumes (agent_control.go) relies on: Linux guests
// get `mount -t virtiofs ...` and macOS guests get `mount_virtiofs ...`.
// Before the fix, the agent dispatched `mount_virtiofs` unconditionally,
// which fails on Linux ("command not found"). This test pins the dispatch
// invariant so a future refactor of virtioFSMountArgs cannot silently
// regress the Linux path.
func TestAgentMountVolumesDispatchPerGuestOS(t *testing.T) {
	tests := []struct {
		name      string
		linuxMode bool
		mount     vmconfig.VolumeMount
		mountAt   string
		wantHead  string // first token: the command that will be exec'd in-guest
	}{
		{
			name:      "linux_rw",
			linuxMode: true,
			mount:     vmconfig.VolumeMount{Tag: "work"},
			mountAt:   "/mnt/work",
			wantHead:  "mount",
		},
		{
			name:      "linux_ro",
			linuxMode: true,
			mount:     vmconfig.VolumeMount{Tag: "work", ReadOnly: true},
			mountAt:   "/mnt/work",
			wantHead:  "mount",
		},
		{
			name:      "macos_rw",
			linuxMode: false,
			mount:     vmconfig.VolumeMount{Tag: "work"},
			mountAt:   "/Volumes/work",
			wantHead:  "mount_virtiofs",
		},
		{
			name:      "macos_ro",
			linuxMode: false,
			mount:     vmconfig.VolumeMount{Tag: "work", ReadOnly: true},
			mountAt:   "/Volumes/work",
			wantHead:  "mount_virtiofs",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := virtioFSMountArgs(tc.mount, tc.mountAt, tc.linuxMode)
			if len(got) == 0 || got[0] != tc.wantHead {
				t.Fatalf("dispatch head = %q, want %q (full args=%v)", firstOrEmpty(got), tc.wantHead, got)
			}
			// Tail must include the tag and mount point in that order so
			// `<cmd> ... <tag> <mountPoint>` shape is preserved across both OSes.
			if got[len(got)-2] != tc.mount.Tag || got[len(got)-1] != tc.mountAt {
				t.Errorf("trailing args = %v, want [..., %q, %q]", got, tc.mount.Tag, tc.mountAt)
			}
		})
	}

	// Sanity: macOS path remains the no-flag form when read-write so
	// `mount_virtiofs <tag> <mountPoint>` is exactly what's exec'd.
	want := []string{"mount_virtiofs", "data", "/Volumes/data"}
	got := virtioFSMountArgs(vmconfig.VolumeMount{Tag: "data"}, "/Volumes/data", false)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("macOS rw mount args = %v, want %v", got, want)
	}
}

func firstOrEmpty(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
