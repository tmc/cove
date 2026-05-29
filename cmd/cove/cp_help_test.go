package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunCpHelpReturnsNil(t *testing.T) {
	for _, alias := range []string{"-h", "--help"} {
		err := runCp(context.Background(), []string{alias}, nil)
		if err != nil {
			t.Fatalf("runCp(%q) = %v, want nil", alias, err)
		}
	}
}

func TestRunCpUnknownFlagReturnsParseError(t *testing.T) {
	err := runCp(context.Background(), []string{"-frobnicate"}, nil)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("err = %v, want parse error", err)
	}
}
