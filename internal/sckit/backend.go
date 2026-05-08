package sckit

import (
	"os"
	"path/filepath"
	"strings"
)

// Backend selects the host screen capture path. Design 041 Slice 3.
type Backend string

const (
	// BackendCGWindow uses the legacy CGWindowListCreateImage path.
	BackendCGWindow Backend = "cgwindow"
	// BackendSCKit uses ScreenCaptureKit with deterministic fallback to
	// CGWindowList on init or capture error.
	BackendSCKit Backend = "sckit"
	// BackendAuto is reserved for Slice 4. In v0.6 it resolves to
	// BackendCGWindow.
	BackendAuto Backend = "auto"
)

// parseKnownBackend returns (backend, true) if s names a known backend.
// Unknown or empty values return (BackendCGWindow, false).
func parseKnownBackend(s string) (Backend, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(BackendSCKit):
		return BackendSCKit, true
	case string(BackendAuto):
		// Reserved; Slice 3 treats auto as cgwindow per spec §1.
		return BackendCGWindow, true
	case string(BackendCGWindow):
		return BackendCGWindow, true
	}
	return BackendCGWindow, false
}

// ParseBackend resolves a string to a Backend. Empty or unknown values
// resolve to BackendCGWindow. Comparison is case-insensitive.
func ParseBackend(s string) Backend {
	b, _ := parseKnownBackend(s)
	return b
}

// BackendForVMDir resolves the capture backend for a VM. Per-VM file
// <vmDir>/capture-backend wins over the COVE_CAPTURE_BACKEND env var,
// which wins over the cgwindow default. An unreadable per-VM file or
// unknown value falls through to env, then default.
func BackendForVMDir(vmDir string) Backend {
	if vmDir != "" {
		if data, err := os.ReadFile(filepath.Join(vmDir, "capture-backend")); err == nil {
			if b, ok := parseKnownBackend(string(data)); ok {
				return b
			}
		}
	}
	return ParseBackend(os.Getenv("COVE_CAPTURE_BACKEND"))
}
