package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClipboardRejectsNUL(t *testing.T) {
	for _, text := range []string{"\x00", "before\x00after"} {
		if err := clipboardSetText(text); !errors.Is(err, syscall.EINVAL) {
			t.Errorf("clipboardSetText(%q) = %v, want EINVAL", text, err)
		}
	}
}

func TestClipboardRoundTrip(t *testing.T) {
	// This test replaces all clipboard formats. Run it in a disposable desktop.
	if os.Getenv("COVE_TEST_WINDOWS_CLIPBOARD") != "1" {
		t.Skip("set COVE_TEST_WINDOWS_CLIPBOARD=1 in a disposable Windows desktop")
	}
	old, err := clipboardGetText()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clipboardSetText(old); err != nil {
			t.Errorf("restore clipboard text: %v", err)
		}
	})
	for _, tt := range []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"ascii", "hello, clipboard"},
		{"unicode", "你好 🌍 café"},
		{"multiline", "first\r\nsecond\r\n"},
		{"large", strings.Repeat("hello 🌍 ", 8192)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := clipboardSetText(tt.text); err != nil {
				t.Fatal(err)
			}
			got, err := clipboardGetText()
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.text {
				t.Fatalf("clipboard text differs: got %d bytes, want %d", len(got), len(tt.text))
			}
		})
	}
}

func TestClipboardCLIEmpty(t *testing.T) {
	if os.Getenv("COVE_TEST_CLIPBOARD_CLI_CHILD") == "1" {
		os.Args = []string{os.Args[0], "-clipboard-set-base64="}
		main()
		return
	}
	if os.Getenv("COVE_TEST_WINDOWS_CLIPBOARD") != "1" {
		t.Skip("set COVE_TEST_WINDOWS_CLIPBOARD=1 in a disposable Windows desktop")
	}
	old, err := clipboardGetText()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clipboardSetText(old); err != nil {
			t.Error(err)
		}
	})
	if err := clipboardSetText("clear this text"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClipboardCLIEmpty$")
	cmd.Env = append(os.Environ(), "COVE_TEST_CLIPBOARD_CLI_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clipboard CLI: %v\n%s", err, out)
	}
	if got, err := clipboardGetText(); err != nil || got != "" {
		t.Fatalf("clipboard = %q, %v; want empty text", got, err)
	}
}
