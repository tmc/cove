package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrInjectFlagRequiredWraps(t *testing.T) {
	cases := []error{
		fmt.Errorf("%w: -user", ErrInjectFlagRequired),
		fmt.Errorf("%w: -password", ErrInjectFlagRequired),
	}
	for _, err := range cases {
		if !errors.Is(err, ErrInjectFlagRequired) {
			t.Fatalf("err = %v, want errors.Is(err, ErrInjectFlagRequired)", err)
		}
	}
}
