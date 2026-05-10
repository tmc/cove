package main

import (
	"strings"
	"testing"
)

func TestDiskAttachmentModesUnknownPolicy(t *testing.T) {
	_, _, err := diskAttachmentModes(DiskCachePolicy(99))
	if err == nil || !strings.Contains(err.Error(), "unknown disk cache policy") {
		t.Fatalf("err = %v, want unknown disk cache policy", err)
	}
}

func TestOverrideDiskSyncModeInvalid(t *testing.T) {
	old := diskSyncMode
	t.Cleanup(func() { diskSyncMode = old })
	diskSyncMode = "frobnicate"
	_, err := overrideDiskSyncMode(0)
	if err == nil || !strings.Contains(err.Error(), "invalid -disk-sync") {
		t.Fatalf("err = %v, want invalid -disk-sync", err)
	}
}
