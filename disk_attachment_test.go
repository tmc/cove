package main

import (
	"testing"

	vz "github.com/tmc/apple/virtualization"
)

func TestDiskAttachmentModes(t *testing.T) {
	tests := []struct {
		name        string
		policy      DiskCachePolicy
		wantCaching vz.VZDiskImageCachingMode
		wantSync    vz.VZDiskImageSynchronizationMode
	}{
		{"durable", DiskCacheDurable, vz.VZDiskImageCachingModeCached, vz.VZDiskImageSynchronizationModeFsync},
		{"ephemeral", DiskCacheEphemeral, vz.VZDiskImageCachingModeCached, vz.VZDiskImageSynchronizationModeNone},
		{"read-only", DiskCacheReadOnly, vz.VZDiskImageCachingModeAutomatic, vz.VZDiskImageSynchronizationModeFull},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCaching, gotSync, err := diskAttachmentModes(tt.policy)
			if err != nil {
				t.Fatal(err)
			}
			if gotCaching != tt.wantCaching || gotSync != tt.wantSync {
				t.Fatalf("diskAttachmentModes(%v) = %v, %v, want %v, %v", tt.policy, gotCaching, gotSync, tt.wantCaching, tt.wantSync)
			}
		})
	}
}

func TestOverrideDiskSyncMode(t *testing.T) {
	old := diskSyncMode
	defer func() { diskSyncMode = old }()

	tests := []struct {
		name string
		flag string
		want vz.VZDiskImageSynchronizationMode
	}{
		{"default", "", vz.VZDiskImageSynchronizationModeFsync},
		{"fsync", "fsync", vz.VZDiskImageSynchronizationModeFsync},
		{"none", "none", vz.VZDiskImageSynchronizationModeNone},
		{"full", "full", vz.VZDiskImageSynchronizationModeFull},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diskSyncMode = tt.flag
			got, err := overrideDiskSyncMode(vz.VZDiskImageSynchronizationModeFsync)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("overrideDiskSyncMode = %v, want %v", got, tt.want)
			}
		})
	}
}
