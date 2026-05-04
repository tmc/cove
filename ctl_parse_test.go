package main

import (
	"reflect"
	"testing"
	"time"
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
