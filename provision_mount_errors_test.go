package main

import (
	"errors"
	"strings"
	"testing"
)

func TestDataPartitionNotFoundErrorWraps(t *testing.T) {
	out := "/dev/disk19s1 EFI\n/dev/disk19s2 Macintosh HD\n"
	err := dataPartitionNotFoundError("/dev/disk19", out)
	if err == nil {
		t.Fatal("dataPartitionNotFoundError returned nil")
	}
	if !errors.Is(err, ErrDataPartitionNotFound) {
		t.Fatalf("err = %v, want errors.Is(err, ErrDataPartitionNotFound)", err)
	}
	if !strings.Contains(err.Error(), "/dev/disk19") {
		t.Fatalf("err = %v, want device path", err)
	}
	if !strings.Contains(err.Error(), "Macintosh HD") {
		t.Fatalf("err = %v, want diskutil output", err)
	}
}
