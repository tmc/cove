package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseLogsArgsHelpReturnsErrFlagHelp(t *testing.T) {
	env := commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)}
	for _, alias := range []string{"-h", "--help"} {
		_, err := parseLogsArgs(env, []string{alias})
		if !errors.Is(err, errFlagHelp) {
			t.Fatalf("parseLogsArgs(%q) err = %v, want errFlagHelp", alias, err)
		}
	}
}

func TestParseLogsArgsUnknownFlag(t *testing.T) {
	_, err := parseLogsArgs(commandEnv{Stderr: new(bytes.Buffer)}, []string{"-frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("err = %v, want parse error", err)
	}
}
