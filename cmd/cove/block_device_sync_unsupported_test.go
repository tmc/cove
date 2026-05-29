package main

import (
	"strings"
	"testing"
)

func TestBlockDeviceSyncModeUnsupported(t *testing.T) {
	_, err := blockDeviceSyncMode(blockDeviceSpec{Path: "/dev/rdisk8", Sync: "frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "unsupported sync mode") {
		t.Fatalf("err = %v, want unsupported sync mode", err)
	}
}
