package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClipboardSharingState(t *testing.T) {
	for _, tt := range []struct {
		name           string
		enabled, macOS bool
		user           string
		failed         bool
		native, text   string
	}{
		{"disabled", false, true, "connected", false, "disabled", "disabled"},
		{"other platform", true, false, "connected", false, "unverified", "unavailable"},
		{"waiting for login", true, true, "disconnected", false, "unverified", "waiting-for-user-agent"},
		{"unknown user", true, true, "", false, "unverified", "waiting-for-user-agent"},
		{"connected does not verify native", true, true, "connected", false, "unverified", "ready"},
		{"retry", true, true, "connected", true, "unverified", "retrying"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := clipboardSharingState(tt.enabled, tt.macOS, tt.user, time.Time{}, tt.failed)
			if got.Native != tt.native || got.HostToGuestText != tt.text {
				t.Fatalf("status = %+v", got)
			}
			if tt.enabled && !strings.Contains(got.Summary, "unverified") {
				t.Fatalf("summary claims native health: %s", got.Summary)
			}
		})
	}
}

func TestClipboardMonitorStatus(t *testing.T) {
	var status clipboardMonitorStatus
	status.record(errors.New("private clipboard content"))
	if !status.failed || !status.lastSync.IsZero() {
		t.Fatal("failed operation recorded as successful")
	}
	status.record(nil)
	if status.failed || status.lastSync.IsZero() {
		t.Fatal("successful sync not recorded")
	}
	last := status.lastSync
	status.record(errors.New("unavailable"))
	if !status.failed || status.lastSync != last {
		t.Fatal("failure lost previous successful observation")
	}
}
