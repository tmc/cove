package controlclient

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatDialErrorMissingSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "demo", "control.sock")
	err := FormatDialError(sock, os.ErrNotExist)
	if err == nil {
		t.Fatal("FormatDialError returned nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"vm is not running",
		"control socket not found",
		"cove -vm demo run",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("FormatDialError missing %q in %q", want, msg)
		}
	}
}

func TestFormatDialErrorTimeout(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "demo", "control.sock")
	err := FormatDialError(sock, errors.New("dial unix control.sock: i/o timeout"))
	if err == nil {
		t.Fatal("FormatDialError returned nil")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("FormatDialError = %q, want not-ready guidance", err)
	}
}

func TestLoadTokenFromPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), TokenFileName)
	if err := os.WriteFile(path, []byte(" token-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTokenFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "token-value" {
		t.Fatalf("LoadTokenFromPath = %q, want token-value", got)
	}
}
