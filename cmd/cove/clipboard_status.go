package main

import (
	"sync"
	"time"

	agentstate "github.com/tmc/cove/internal/agent"
)

type clipboardMonitorStatus struct {
	mu       sync.Mutex
	lastSync time.Time
	failed   bool
}

func (s *clipboardMonitorStatus) record(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = err != nil
	if err == nil {
		s.lastSync = time.Now()
	}
}

type clipboardSharingStatus struct {
	Enabled         bool       `json:"enabled"`
	Native          string     `json:"native"`
	HostToGuestText string     `json:"hostToGuestText"`
	UserAgent       string     `json:"userAgent"`
	LastTextSync    *time.Time `json:"lastTextSync,omitempty"`
	TextLimit       int        `json:"textLimitBytes"`
	Summary         string     `json:"summary"`
}

func clipboardSharingState(enabled, macOS bool, user string, lastSync time.Time, failed bool) clipboardSharingStatus {
	status := clipboardSharingStatus{Enabled: enabled, Native: "unverified", HostToGuestText: "unavailable", UserAgent: user, TextLimit: clipboardTextLimit}
	if enabled && macOS && !lastSync.IsZero() {
		status.LastTextSync = &lastSync
	}
	switch {
	case !enabled:
		status.Native = "disabled"
		status.HostToGuestText = "disabled"
		status.Summary = "Clipboard sharing is disabled."
	case !macOS:
		status.Summary = "Native SPICE clipboard transfer is unverified; test copying and pasting in both directions."
	case user != "connected":
		status.HostToGuestText = "waiting-for-user-agent"
		status.Summary = "Text sharing waits for the user agent; finish logging in and check cove ctl agent-status. Native SPICE transfer is unverified."
	case failed:
		status.HostToGuestText = "retrying"
		status.Summary = "Host-to-guest text sync failed and will retry; check the user agent. Native SPICE transfer is unverified."
	default:
		status.HostToGuestText = "ready"
		status.Summary = "Host-to-guest plain text fallback is ready (up to 1 MiB). Native SPICE transfer is unverified."
	}
	return status
}

func (s *ControlServer) clipboardStatus() clipboardSharingStatus {
	s.mu.Lock()
	enabled := s.runConfig.EnableClipboard && s.hostConfig.RuntimeProfile != "minimal"
	s.mu.Unlock()
	s.clipboardMonitor.mu.Lock()
	lastSync, failed := s.clipboardMonitor.lastSync, s.clipboardMonitor.failed
	s.clipboardMonitor.mu.Unlock()
	return clipboardSharingState(enabled, agentstate.Platform(s.vmDir) == agentstate.PlatformMacOS, s.bridge.HealthSnapshot().UserStatus, lastSync, failed)
}
