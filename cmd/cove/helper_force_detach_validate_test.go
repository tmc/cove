package main

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateForceDetachRequestRejectsBadInputs(t *testing.T) {
	oldFind := helperFindAttachedDisk
	t.Cleanup(func() { helperFindAttachedDisk = oldFind })
	helperFindAttachedDisk = func(string) (string, bool, error) {
		t.Fatal("helperFindAttachedDisk should not be called for input-validation rejects")
		return "", false, nil
	}

	tests := []struct {
		name    string
		device  string
		path    string
		wantSub string
	}{
		{name: "empty device", device: "  ", path: "/Users/me/.vz/vms/x/disk.img", wantSub: "device is required"},
		{name: "non /dev/disk device", device: "/dev/null", path: "/Users/me/.vz/vms/x/disk.img", wantSub: "is not a /dev/disk device"},
		{name: "relative disk path", device: "/dev/disk5", path: "relative/disk.img", wantSub: "must be absolute"},
		{name: "non disk.img basename", device: "/dev/disk5", path: "/Users/me/.vz/vms/x/other.img", wantSub: "is not a cove disk image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := validateForceDetachRequest(&forceDetachRequest{Device: tt.device, DiskPath: tt.path})
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want substring %q", err, tt.wantSub)
			}
		})
	}
}

func TestValidateForceDetachRequestSurfacesFindError(t *testing.T) {
	oldFind := helperFindAttachedDisk
	t.Cleanup(func() { helperFindAttachedDisk = oldFind })
	sentinel := errors.New("hdiutil info failed")
	helperFindAttachedDisk = func(string) (string, bool, error) {
		return "", false, sentinel
	}
	_, _, err := validateForceDetachRequest(&forceDetachRequest{
		Device:   "/dev/disk5",
		DiskPath: "/Users/me/.vz/vms/x/disk.img",
	})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapping %v", err, sentinel)
	}
}

func TestValidateForceDetachRequestRejectsNotAttached(t *testing.T) {
	oldFind := helperFindAttachedDisk
	t.Cleanup(func() { helperFindAttachedDisk = oldFind })
	helperFindAttachedDisk = func(string) (string, bool, error) {
		return "", false, nil
	}
	_, _, err := validateForceDetachRequest(&forceDetachRequest{
		Device:   "/dev/disk5",
		DiskPath: "/Users/me/.vz/vms/x/disk.img",
	})
	if err == nil || !strings.Contains(err.Error(), "is not attached") {
		t.Fatalf("err = %v, want 'is not attached'", err)
	}
}
