package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestDetachDiskForPathUsesHelperWithoutManualCommand(t *testing.T) {
	oldEnsure := ensureDetachedHook
	oldHelperInstalled := helperInstalled
	oldForce := forceDetachViaHelperHook
	t.Cleanup(func() {
		ensureDetachedHook = oldEnsure
		helperInstalled = oldHelperInstalled
		forceDetachViaHelperHook = oldForce
	})
	ensureDetachedHook = func(string) error {
		return fmt.Errorf("auto-detach failed: disk busy")
	}
	helperInstalled = func() bool { return true }
	var gotDevice, gotDiskPath string
	forceDetachViaHelperHook = func(device, diskPath string) error {
		gotDevice, gotDiskPath = device, diskPath
		return nil
	}

	out := captureStdout(t, func() error {
		detachDiskForPath("/dev/disk23", "/Users/tmc/.vz/vms/test/disk.img")
		return nil
	})
	if gotDevice != "/dev/disk23" || gotDiskPath != "/Users/tmc/.vz/vms/test/disk.img" {
		t.Fatalf("forceDetachViaHelper(%q, %q)", gotDevice, gotDiskPath)
	}
	if strings.Contains(out, "Manual fix:") || strings.Contains(out, "hdiutil detach") {
		t.Fatalf("output contains manual command:\n%s", out)
	}
	if !strings.Contains(out, "Detached via cove-helper.") {
		t.Fatalf("output missing helper success:\n%s", out)
	}
}

func TestDetachDiskForPathPrintsManualCommandWithoutHelper(t *testing.T) {
	oldEnsure := ensureDetachedHook
	oldHelperInstalled := helperInstalled
	t.Cleanup(func() {
		ensureDetachedHook = oldEnsure
		helperInstalled = oldHelperInstalled
	})
	ensureDetachedHook = func(string) error {
		return fmt.Errorf("auto-detach failed: disk busy")
	}
	helperInstalled = func() bool { return false }

	out := captureStdout(t, func() error {
		detachDiskForPath("/dev/disk23", "/Users/tmc/.vz/vms/test/disk.img")
		return nil
	})
	if !strings.Contains(out, "Manual fix: hdiutil detach /dev/disk23 -force") {
		t.Fatalf("output missing manual fallback:\n%s", out)
	}
}

// diskutil list output matching the VM-lifecycle report (finding 3): the grok-
// lab image is attached as disk27 with its APFS container synthesized as disk30
// and Data volume disk30s5, while an UNRELATED disk23 (with its own container
// disk25 and Data volume disk25s5) is already attached. Selection must anchor on
// the Physical Store back-reference and pick disk30 / disk30s5, never disk23.
const diskutilListTwoImages = `/dev/disk23 (disk image):
   #:                       TYPE NAME                    SIZE       IDENTIFIER
   0:      GUID_partition_scheme                        +68.7 GB    disk23
   2:                 Apple_APFS Container disk25         62.8 GB    disk23s2

/dev/disk25 (synthesized):
   #:                       TYPE NAME                    SIZE       IDENTIFIER
   0:      APFS Container Scheme -                      +62.8 GB    disk25
                                 Physical Store disk23s2
   4:                APFS Volume Data                    11.0 GB    disk25s5

/dev/disk27 (disk image):
   #:                       TYPE NAME                    SIZE       IDENTIFIER
   0:      GUID_partition_scheme                        +68.7 GB    disk27
   2:                 Apple_APFS Container disk30         62.8 GB    disk27s2

/dev/disk30 (synthesized):
   #:                       TYPE NAME                    SIZE       IDENTIFIER
   0:      APFS Container Scheme -                      +62.8 GB    disk30
                                 Physical Store disk27s2
   1:                APFS Volume Macintosh HD            10.5 GB    disk30s1
   5:                APFS Volume Data                    11.4 GB    disk30s5
`

func TestFindAPFSContainerForDevice(t *testing.T) {
	tests := []struct {
		name   string
		output string
		device string
		want   string
	}{
		{"our image disk27 -> disk30", diskutilListTwoImages, "/dev/disk27", "disk30"},
		{"unrelated disk23 -> disk25", diskutilListTwoImages, "/dev/disk23", "disk25"},
		{"absent device -> empty", diskutilListTwoImages, "/dev/disk99", ""},
		// disk2 must NOT match disk27's "Physical Store disk27s2" via a prefix.
		{"no false prefix match", diskutilListTwoImages, "/dev/disk2", ""},
		{"empty device -> empty", diskutilListTwoImages, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findAPFSContainerForDevice(tt.output, tt.device); got != tt.want {
				t.Errorf("findAPFSContainerForDevice(_, %q) = %q, want %q", tt.device, got, tt.want)
			}
		})
	}
}

// container disk30 listing, as `diskutil list /dev/disk30` would print it.
const container30Listing = `/dev/disk30 (synthesized):
   #:                       TYPE NAME                    SIZE       IDENTIFIER
   0:      APFS Container Scheme -                      +62.8 GB    disk30
                                 Physical Store disk27s2
   1:                APFS Volume Macintosh HD            10.5 GB    disk30s1
   3:                APFS Volume Preboot                 6.2 MB     disk30s3
   5:                APFS Volume Data                    11.4 GB    disk30s5
`

func TestFindDataVolumeInContainer(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		container string
		want      string
	}{
		{"data volume in disk30", container30Listing, "disk30", "/dev/disk30s5"},
		// A volume from another container must not be accepted even if it
		// appears in the text (defense against cross-container selection).
		{"reject foreign container data", container30Listing, "disk40", ""},
		{"no data volume", "/dev/disk30 (synthesized):\n   1: APFS Volume Macintosh HD  disk30s1\n", "disk30", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findDataVolumeInContainer(tt.output, tt.container); got != tt.want {
				t.Errorf("findDataVolumeInContainer(_, %q) = %q, want %q", tt.container, got, tt.want)
			}
		})
	}
}

// End-to-end of the pure selection path for the report's exact topology:
// device disk27 must resolve to container disk30 and Data volume disk30s5,
// never touching the unrelated disk23/disk25.
func TestFinding3_SelectsOwnImageNotConcurrentDisk(t *testing.T) {
	container := findAPFSContainerForDevice(diskutilListTwoImages, "/dev/disk27")
	if container != "disk30" {
		t.Fatalf("container = %q, want disk30", container)
	}
	data := findDataVolumeInContainer(container30Listing, container)
	if data != "/dev/disk30s5" {
		t.Fatalf("data volume = %q, want /dev/disk30s5", data)
	}
	if container == "disk25" || data == "/dev/disk25s5" {
		t.Fatalf("selected the unrelated concurrent disk23 chain: container=%q data=%q", container, data)
	}
}

func TestBaseDiskDevice(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/dev/disk27s2", "/dev/disk27"},
		{"/dev/disk27", "/dev/disk27"},
		{"/dev/disk30s5", "/dev/disk30"},
		{"/dev/disk5", "/dev/disk5"},
		{"disk27s2", "disk27s2"}, // not /dev-prefixed: unchanged
		{"", ""},
	}
	for _, tt := range tests {
		if got := baseDiskDevice(tt.in); got != tt.want {
			t.Errorf("baseDiskDevice(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
