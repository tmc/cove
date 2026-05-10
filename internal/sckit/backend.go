package sckit

import (
	"log/slog"
	"os"
	"strings"
	"sync"
)

// Backend selects the host screen capture path. Design 041 Slice 4.
type Backend string

const (
	// BackendSCKit uses ScreenCaptureKit.
	BackendSCKit Backend = "sckit"
)

var warnCGWindowEnvOnce sync.Once

// ParseBackend resolves a string to a Backend. SCKit is always selected.
// The legacy cgwindow value logs a one-release compatibility warning.
func ParseBackend(s string) Backend {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cgwindow":
		warnCGWindowEnvOnce.Do(func() {
			slog.Warn("COVE_CAPTURE_BACKEND=cgwindow is no longer supported; using sckit. This warning will become a hard error in v0.7.")
		})
	}
	return BackendSCKit
}

// BackendForVMDir resolves the capture backend for a VM.
func BackendForVMDir(_ string) Backend {
	return ParseBackend(os.Getenv("COVE_CAPTURE_BACKEND"))
}
