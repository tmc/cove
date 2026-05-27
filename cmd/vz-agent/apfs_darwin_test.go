package main

import (
	"strings"
	"testing"
)

func TestParseDiskutilAPFSContainer(t *testing.T) {
	out := `
   File System Personality:   APFS
   APFS Container:            disk3
   Container Total Space:     63.9 GB (63888117760 Bytes)
`
	got, err := parseDiskutilAPFSContainer(out)
	if err != nil {
		t.Fatal(err)
	}
	if got != "disk3" {
		t.Fatalf("container = %q, want disk3", got)
	}
	if got := parseDiskutilBytesLine(out, "Container Total Space:"); got != 63888117760 {
		t.Fatalf("bytes = %d, want 63888117760", got)
	}
}

func TestParseDiskutilAPFSPhysicalStore(t *testing.T) {
	out := `
APFS Container (1 found)
|
+-- Container disk3
    ====================================================
    APFS Container Reference:     disk3
    Size (Capacity Ceiling):      58.5 GB (58490892288 Bytes)
    Capacity In Use By Volumes:   56.6 GB (56587960320 Bytes)
    APFS Physical Store Disk:     disk0s2
`
	got, err := parseDiskutilAPFSPhysicalStore(out)
	if err != nil {
		t.Fatal(err)
	}
	if got != "disk0s2" {
		t.Fatalf("physical store = %q, want disk0s2", got)
	}
}

func TestParseDiskutilPartitionID(t *testing.T) {
	disk, part, err := parseDiskutilPartitionID("disk0s2")
	if err != nil {
		t.Fatal(err)
	}
	if disk != "disk0" || part != 2 {
		t.Fatalf("partition = %s %d, want disk0 2", disk, part)
	}
	if _, _, err := parseDiskutilPartitionID("disk0"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseDiskutilRecoveryBlocker(t *testing.T) {
	out := `
/dev/disk0 (internal, physical):
   #:                       TYPE NAME                    SIZE       IDENTIFIER
   0:      GUID_partition_scheme                        *137.4 GB   disk0
   1:             Apple_APFS_ISC                         524.3 MB   disk0s1
   2:                 Apple_APFS Container disk3         58.5 GB    disk0s2
   3:        Apple_APFS_Recovery Container disk2         5.4 GB     disk0s3
                    (free space)                         73.0 GB    -
`
	got := parseDiskutilRecoveryBlocker(out, "disk0", 2)
	if got != "disk0s3" {
		t.Fatalf("blocker = %q, want disk0s3", got)
	}
	if got := parseDiskutilRecoveryBlocker(out, "disk0", 3); got != "" {
		t.Fatalf("blocker = %q, want none", got)
	}
}

func TestDiskutilErrorPreservesOutput(t *testing.T) {
	err := diskutilError("resize APFS container", diskutilResult{
		stdout: []byte("started APFS operation\n"),
		stderr: []byte("target disk is too small\n"),
	}, errTest("exit status 1"))
	got := err.Error()
	for _, want := range []string{"resize APFS container", "stdout: started APFS operation", "stderr: target disk is too small"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q: %q", want, got)
		}
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
