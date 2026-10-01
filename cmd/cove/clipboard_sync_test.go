package main

import (
	"errors"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmrun"
)

func TestClipboardPushState(t *testing.T) {
	for _, tt := range []struct {
		name string
		text clipboardText
		want int
	}{
		{"text", clipboardText{change: 1, text: "host text", available: true}, 1},
		{"empty text", clipboardText{change: 1, available: true}, 1},
		{"non-text", clipboardText{change: 1}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var state clipboardPushState
			calls := 0
			push := func(value string) error {
				calls++
				if value != tt.text.text {
					t.Fatalf("pushed %q", value)
				}
				return nil
			}
			for i := 0; i < 2; i++ {
				if err := state.sync(tt.text, push); err != nil {
					t.Fatal(err)
				}
			}
			if calls != tt.want {
				t.Fatalf("calls = %d, want %d", calls, tt.want)
			}
		})
	}
}

func TestClipboardPushRetriesLatestAfterFailure(t *testing.T) {
	var state clipboardPushState
	old := clipboardText{change: 1, text: "old", available: true}
	if err := state.sync(old, func(string) error { return errors.New("user agent disconnected") }); err == nil {
		t.Fatal("missing failure")
	}
	latest := clipboardText{change: 2, text: "latest", available: true}
	var pushed string
	if err := state.sync(latest, func(value string) error { pushed = value; return nil }); err != nil {
		t.Fatal(err)
	}
	if pushed != "latest" {
		t.Fatalf("pushed %q", pushed)
	}
	if err := state.sync(latest, func(string) error { t.Fatal("repeated completed push"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestClipboardPushRetriesSameChange(t *testing.T) {
	var state clipboardPushState
	text := clipboardText{change: 1, text: "retry me", available: true}
	calls := 0
	push := func(string) error {
		calls++
		if calls == 1 {
			return errors.New("disconnected")
		}
		return nil
	}
	if err := state.sync(text, push); err == nil {
		t.Fatal("missing first failure")
	}
	if err := state.sync(text, push); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestClipboardPushSkipsOversizedText(t *testing.T) {
	var state clipboardPushState
	text := clipboardText{change: 1, text: string(make([]byte, clipboardTextLimit+1)), available: true}
	if err := state.sync(text, func(string) error { t.Fatal("pushed oversized text"); return nil }); err != nil {
		t.Fatal(err)
	}
	text.change++
	text.text = "small"
	called := false
	if err := state.sync(text, func(string) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("oversized clipboard suppressed later small copy")
	}
}

func TestClipboardMonitorDisabled(t *testing.T) {
	for _, tt := range []struct {
		name    string
		enabled bool
		profile string
	}{
		{"explicit opt-out", false, "full"},
		{"minimal runtime", true, "minimal"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := &ControlServer{runConfig: vmrun.RunConfig{EnableClipboard: tt.enabled}, hostConfig: vmrun.HostConfig{RuntimeProfile: tt.profile}}
			server.startLifecycleContext()
			defer server.shutdownLifecycleContext()
			done := make(chan struct{})
			go func() { defer close(done); server.monitorHostClipboard() }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("clipboard monitor started despite disabled sharing")
			}
		})
	}
}
