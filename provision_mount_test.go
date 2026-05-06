package main

import (
	"strings"
	"testing"
)

func TestDataPartitionNotFoundErrorIncludesDiskutilList(t *testing.T) {
	diskutilList := `/dev/disk23 (disk image):
   #:                       TYPE NAME                    SIZE       IDENTIFIER
   0:      GUID_partition_scheme                        *80.0 GB    disk23
`
	err := dataPartitionNotFoundError("/dev/disk23", diskutilList)
	if err == nil {
		t.Fatal("dataPartitionNotFoundError returned nil")
	}
	for _, want := range []string{
		"could not find Data partition for disk /dev/disk23",
		"diskutil list output:",
		"GUID_partition_scheme",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q\n%v", want, err)
		}
	}
}
