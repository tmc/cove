package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func diskLayoutFixture(device, scheme, partition string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>system-entities</key><array>
 <dict><key>dev-entry</key><string>%s</string><key>content-hint</key><string>%s</string></dict>
 <dict><key>dev-entry</key><string>disk5s2</string><key>content-hint</key><string>%s</string></dict>
 </array></dict></plist>`, device, scheme, partition))
}

func TestInstalledDiskLayout(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		device  string
		wantErr bool
	}{
		{"diskutil ASIF", diskLayoutFixture("disk5", "GUID_partition_scheme", "Apple_APFS_Container"), "/dev/disk5", false},
		{"hdiutil raw", diskLayoutFixture("/dev/disk5", "GUID_partition_scheme", "Apple_APFS"), "/dev/disk5", false},
		{"zero logical disk", diskLayoutFixture("disk5", "", ""), "/dev/disk5", true},
		{"missing GPT", diskLayoutFixture("disk5", "", "Apple_APFS"), "/dev/disk5", true},
		{"missing APFS", diskLayoutFixture("disk5", "GUID_partition_scheme", ""), "/dev/disk5", true},
		{"missing device", diskLayoutFixture("", "GUID_partition_scheme", "Apple_APFS"), "", true},
		{"malformed", []byte("not a plist"), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device, err := installedDiskLayout(tt.data)
			if device != tt.device || (err != nil) != tt.wantErr {
				t.Fatalf("layout = %q, %v; want %q, error %v", device, err, tt.device, tt.wantErr)
			}
		})
	}
}

func TestCheckInstalledDisk(t *testing.T) {
	tests := []struct {
		name                                             string
		legacy, badLayout, attachFail, ejectFail, cancel bool
	}{
		{name: "diskutil"},
		{name: "hdiutil", legacy: true},
		{name: "invalid layout is ejected", badLayout: true},
		{name: "attach fails", attachFail: true},
		{name: "eject fails", ejectFail: true},
		{name: "cancel after attach still ejects", cancel: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls [][]string
			run := func(ctx context.Context, args ...string) ([]byte, error) {
				calls = append(calls, append([]string(nil), args...))
				if args[len(args)-1] == "--help" {
					if tt.legacy {
						return nil, errors.New("unsupported")
					}
					return nil, nil
				}
				if args[1] == "eject" || args[1] == "detach" {
					if ctx.Err() != nil {
						t.Fatal("cleanup inherited canceled context")
					}
					if tt.ejectFail {
						return nil, errors.New("busy")
					}
					return nil, nil
				}
				if tt.attachFail {
					return nil, errors.New("cannot attach")
				}
				if tt.cancel {
					cancel()
				}
				if tt.badLayout {
					return diskLayoutFixture("disk5", "", ""), nil
				}
				return diskLayoutFixture("disk5", "GUID_partition_scheme", "Apple_APFS"), nil
			}
			err := checkInstalledDisk(ctx, "/test/disk with spaces.img", run)
			wantErr := tt.badLayout || tt.attachFail || tt.ejectFail
			if (err != nil) != wantErr {
				t.Fatalf("error = %v; want error %v", err, wantErr)
			}
			wantAttach := []string{"diskutil", "image", "attach", "--readOnly", "--noMount", "--plist", "/test/disk with spaces.img"}
			wantDetach := []string{"diskutil", "eject", "/dev/disk5"}
			if tt.legacy {
				wantAttach = []string{"hdiutil", "attach", "-readonly", "-nomount", "-plist", "/test/disk with spaces.img"}
				wantDetach = []string{"hdiutil", "detach", "/dev/disk5"}
			}
			if !reflect.DeepEqual(calls[1], wantAttach) {
				t.Fatalf("attach = %q; want %q", calls[1], wantAttach)
			}
			if tt.attachFail {
				if len(calls) != 2 {
					t.Fatalf("unexpected cleanup: %q", calls)
				}
				return
			}
			if len(calls) != 3 || !reflect.DeepEqual(calls[2], wantDetach) {
				t.Fatalf("calls = %q", calls)
			}
			if tt.ejectFail && !strings.Contains(err.Error(), "eject installed disk") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
