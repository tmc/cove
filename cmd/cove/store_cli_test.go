package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHandleStoreCommandNoArgsUsesEnvStderr(t *testing.T) {
	env := commandTestEnv()
	err := handleStoreCommand(env, nil)
	if err == nil || !strings.Contains(err.Error(), "store command required") {
		t.Fatalf("handleStoreCommand(nil) error = %v, want store command required", err)
	}
	if got := env.Stderr.(*bytes.Buffer).String(); !strings.Contains(got, "Usage: cove store") {
		t.Fatalf("stderr = %q, want store usage", got)
	}
	if got := env.Stdout.(*bytes.Buffer).String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
}

func TestHandleStoreGCHelpUsesEnvStderr(t *testing.T) {
	env := commandTestEnv()
	if err := handleStoreCommand(env, []string{"gc", "-h"}); err != nil {
		t.Fatalf("handleStoreCommand(gc -h): %v", err)
	}
	if got := env.Stderr.(*bytes.Buffer).String(); !strings.Contains(got, "Usage: cove store gc") {
		t.Fatalf("stderr = %q, want store gc usage", got)
	}
	if got := env.Stdout.(*bytes.Buffer).String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
}

func TestHandleStoreGCDryRunUsesEnvStdout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := commandTestEnv()
	if err := handleStoreCommand(env, []string{"gc", "-dry-run"}); err != nil {
		t.Fatalf("handleStoreCommand(gc -dry-run): %v", err)
	}
	if got := env.Stdout.(*bytes.Buffer).String(); !strings.Contains(got, "Store GC dry run: would delete") {
		t.Fatalf("stdout = %q, want dry run summary", got)
	}
	if got := env.Stderr.(*bytes.Buffer).String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}
