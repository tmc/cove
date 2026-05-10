package main

import (
	"strings"
	"testing"
)

func TestHandleSecretCommandEmptyAndHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"-h"}, {"--help"}, {"help"}} {
		if err := handleSecretCommand(args); err != nil {
			t.Fatalf("handleSecretCommand(%v) = %v, want nil", args, err)
		}
	}
}

func TestHandleSecretCommandUnknown(t *testing.T) {
	err := handleSecretCommand([]string{"frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "unknown secret command") {
		t.Fatalf("err = %v, want unknown secret command", err)
	}
}
