package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrAuthorizationNoTTYWraps(t *testing.T) {
	err := fmt.Errorf("%w; re-run the provisioning command with sudo", ErrAuthorizationNoTTY)
	if !errors.Is(err, ErrAuthorizationNoTTY) {
		t.Fatalf("err = %v, want errors.Is(err, ErrAuthorizationNoTTY)", err)
	}
}

func TestErrAuthorizationOnUIThreadWraps(t *testing.T) {
	err := fmt.Errorf("%w; run provisioning from a worker goroutine", ErrAuthorizationOnUIThread)
	if !errors.Is(err, ErrAuthorizationOnUIThread) {
		t.Fatalf("err = %v, want errors.Is(err, ErrAuthorizationOnUIThread)", err)
	}
}

func TestErrAuthorizationDistinct(t *testing.T) {
	if errors.Is(ErrAuthorizationNoTTY, ErrAuthorizationOnUIThread) {
		t.Fatal("NoTTY should not match OnUIThread")
	}
	if errors.Is(ErrAuthorizationOnUIThread, ErrAuthorizationNoTTY) {
		t.Fatal("OnUIThread should not match NoTTY")
	}
}
