package main

import (
	"os"
	"strings"
	"testing"
)

func TestValidateBlockDevicePathRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		readOnly bool
		want     string
	}{
		{"relative", "rdisk8", false, "must be absolute"},
		{"non dev", "/tmp/rdisk8", false, "not under /dev"},
		{"writable disk", "/dev/disk8", false, "must use /dev/rdiskN"},
		{"read only regular disk spelling", "/dev/disk8", true, ""},
		{"read only raw disk spelling", "/dev/rdisk8", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBlockDevicePath(tt.path, tt.readOnly)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validateBlockDevicePath: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateBlockDevicePath error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestValidateBlockDeviceNodeRejectsRegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "disk")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	err = validateBlockDeviceNode(f.Name())
	if err == nil || !strings.Contains(err.Error(), "is not a device node") {
		t.Fatalf("validateBlockDeviceNode error = %v, want regular-file rejection", err)
	}
}

func TestValidateBlockDeviceUnmountedRejectsMountedRW(t *testing.T) {
	old := diskutilInfoPlist
	t.Cleanup(func() { diskutilInfoPlist = old })
	diskutilInfoPlist = func(string) ([]byte, error) {
		return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Mounted</key>
	<true/>
</dict>
</plist>`), nil
	}

	err := validateBlockDeviceUnmounted("/dev/rdisk8")
	if err == nil || !strings.Contains(err.Error(), "is mounted") {
		t.Fatalf("validateBlockDeviceUnmounted error = %v, want mounted rejection", err)
	}
}

func TestValidateBlockDeviceUnmountedAcceptsUnmountedRW(t *testing.T) {
	old := diskutilInfoPlist
	t.Cleanup(func() { diskutilInfoPlist = old })
	diskutilInfoPlist = func(string) ([]byte, error) {
		return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Mounted</key>
	<false/>
</dict>
</plist>`), nil
	}

	if err := validateBlockDeviceUnmounted("/dev/rdisk8"); err != nil {
		t.Fatal(err)
	}
}
