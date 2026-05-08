package controlclient

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatDialErrorNil(t *testing.T) {
	if err := FormatDialError("/tmp/x.sock", nil); err != nil {
		t.Fatalf("FormatDialError(nil) = %v, want nil", err)
	}
}

func TestFormatDialErrorConnectionRefusedNoSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "demo", "control.sock")
	err := FormatDialError(sock, errors.New("dial unix: connection refused"))
	if err == nil {
		t.Fatal("FormatDialError returned nil")
	}
	msg := err.Error()
	for _, want := range []string{`vm "demo" is not running`, "cove -vm demo run"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("FormatDialError missing %q in %q", want, msg)
		}
	}
}

func TestFormatDialErrorConnectionRefusedSocketExists(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "control.sock")
	if err := os.WriteFile(sock, nil, 0644); err != nil {
		t.Fatal(err)
	}
	err := FormatDialError(sock, errors.New("connection refused"))
	if err == nil {
		t.Fatal("FormatDialError returned nil")
	}
	if !strings.Contains(err.Error(), "not accepting connections") {
		t.Fatalf("FormatDialError = %q, want not-accepting guidance", err)
	}
}

func TestFormatDialErrorGeneric(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "demo", "control.sock")
	wrapped := errors.New("permission denied")
	err := FormatDialError(sock, wrapped)
	if err == nil {
		t.Fatal("FormatDialError returned nil")
	}
	if !errors.Is(err, wrapped) {
		t.Fatalf("FormatDialError did not wrap original: %v", err)
	}
	if !strings.Contains(err.Error(), "connect to control socket") {
		t.Fatalf("FormatDialError = %q, want connect prefix", err)
	}
}

func TestRunHintForSocket(t *testing.T) {
	tests := []struct {
		name string
		sock string
		want string
	}{
		{"named_vm", "/Users/x/.vz/vms/demo/control.sock", "cove -vm demo run"},
		{"empty", "", "cove run"},
		{"root", "/control.sock", "cove run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RunHintForSocket(tt.sock); got != tt.want {
				t.Fatalf("RunHintForSocket(%q) = %q, want %q", tt.sock, got, tt.want)
			}
		})
	}
}

func TestLoadTokenFromPathMissing(t *testing.T) {
	_, err := LoadTokenFromPath(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("LoadTokenFromPath missing: expected error")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("LoadTokenFromPath err = %v, want IsNotExist", err)
	}
}

func TestLoadTokenFromPathEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), TokenFileName)
	if err := os.WriteFile(path, []byte("\n  \n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTokenFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("LoadTokenFromPath = %q, want empty", got)
	}
}
