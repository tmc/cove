package controlclient

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewPrefersEnvToken(t *testing.T) {
	t.Setenv(TokenEnvVar, "  env-token  ")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TokenFileName), []byte("file-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(filepath.Join(dir, "control.sock"))
	if c.authToken != "env-token" {
		t.Fatalf("authToken = %q, want env-token", c.authToken)
	}
	if c.timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", c.timeout)
	}
}

func TestNewFallsBackToTokenFile(t *testing.T) {
	t.Setenv(TokenEnvVar, "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TokenFileName), []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New(filepath.Join(dir, "control.sock"))
	if c.authToken != "file-token" {
		t.Fatalf("authToken = %q, want file-token", c.authToken)
	}
}

func TestNewWithoutTokenSourcesIsEmpty(t *testing.T) {
	t.Setenv(TokenEnvVar, "")
	dir := t.TempDir()
	c := New(filepath.Join(dir, "control.sock"))
	if c.authToken != "" {
		t.Fatalf("authToken = %q, want empty", c.authToken)
	}
	if c.socketPath == "" {
		t.Fatal("socketPath empty")
	}
}
