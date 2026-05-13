package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestParseClickMenuOptions(t *testing.T) {
	menu, item, timeout, err := parseClickMenuOptions([]string{"-timeout", "5s", "Utilities", "Terminal"})
	if err != nil {
		t.Fatalf("parseClickMenuOptions error = %v", err)
	}
	if menu != "Utilities" {
		t.Fatalf("menu = %q, want %q", menu, "Utilities")
	}
	if item != "Terminal" {
		t.Fatalf("item = %q, want %q", item, "Terminal")
	}
	if timeout != 5*time.Second {
		t.Fatalf("timeout = %s, want %s", timeout, 5*time.Second)
	}
}

func TestParseClickMenuOptionsInvalid(t *testing.T) {
	if _, _, _, err := parseClickMenuOptions([]string{"Utilities"}); err == nil {
		t.Fatalf("expected error for missing menu item argument")
	}
}

func TestParseGUITerminalOptions(t *testing.T) {
	user, cmd, err := parseGUITerminalOptions([]string{"--user", "desk", "--", "bash", "-c", "echo ok"})
	if err != nil {
		t.Fatal(err)
	}
	if user != "desk" {
		t.Fatalf("user = %q, want desk", user)
	}
	want := []string{"bash", "-c", "echo ok"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestParseGUITerminalOptionsRejectsMissingCommand(t *testing.T) {
	if _, _, err := parseGUITerminalOptions([]string{"--user", "desk", "--"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestCtlCommandSuggestsVMFlagForWrongOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(vmconfig.BaseDir(), "linux-vm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ctlCommand([]string{"linux-vm", "stop"})
	if err == nil {
		t.Fatal("ctlCommand succeeded, want wrong-order hint")
	}
	for _, want := range []string{"did you mean", "cove ctl -vm linux-vm stop"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}

func TestCtlReceiveErrorHintsAgentStatus(t *testing.T) {
	err := ctlReceiveError("/tmp/vm/control.sock", "agent-exec", os.ErrDeadlineExceeded)
	if err == nil {
		t.Fatal("ctlReceiveError returned nil")
	}
	for _, want := range []string{"receive agent-exec response", "guest agent may be unavailable", "agent-status"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}
