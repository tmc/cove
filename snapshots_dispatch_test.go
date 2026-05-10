package main

import "testing"

func TestHandleDiskSnapshotCommandHelpAndEmpty(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		if err := handleDiskSnapshotCommand(args); err != nil {
			t.Fatalf("handleDiskSnapshotCommand(%v) error = %v, want nil", args, err)
		}
	}
}
