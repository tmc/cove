package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrIPSWTooSmallWraps(t *testing.T) {
	err := fmt.Errorf("%w: have 0.5 GB, want >= 1.0 GB", ErrIPSWTooSmall)
	if !errors.Is(err, ErrIPSWTooSmall) {
		t.Fatalf("err = %v, want errors.Is(err, ErrIPSWTooSmall)", err)
	}
	if !strings.Contains(err.Error(), "0.5 GB") {
		t.Fatalf("err = %v, want size hint", err)
	}
}

func TestIPSWMinSizeIs1GB(t *testing.T) {
	if ipswMinSize != int64(1<<30) {
		t.Fatalf("ipswMinSize = %d, want %d", ipswMinSize, int64(1<<30))
	}
}
