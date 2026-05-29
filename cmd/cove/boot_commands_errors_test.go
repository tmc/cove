package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrBootTimeoutWraps(t *testing.T) {
	cases := []error{
		fmt.Errorf("%w: text %q not found", ErrBootTimeout, "Continue"),
		fmt.Errorf("%w: click target %q not found", ErrBootTimeout, "Next"),
		fmt.Errorf("%w: host-click target %q not found", ErrBootTimeout, "OK"),
		fmt.Errorf("%w: activate Recovery Startup Options", ErrBootTimeout),
		fmt.Errorf("%w: leave Recovery language page", ErrBootTimeout),
		fmt.Errorf("%w: click menu item %q from menu %q", ErrBootTimeout, "Restart", "Apple"),
	}
	for _, err := range cases {
		if !errors.Is(err, ErrBootTimeout) {
			t.Fatalf("err = %v, want errors.Is(err, ErrBootTimeout)", err)
		}
	}
}
