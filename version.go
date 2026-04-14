package main

import (
	"fmt"
	"runtime/debug"
)

// version, commit, and date are set by goreleaser or ldflags at build time.
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc1234 -X main.date=2025-01-01T00:00:00Z"
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// resolveVersion populates commit and date from build info if still at defaults.
func resolveVersion() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 8 {
					commit = s.Value[:8]
				}
				if s.Key == "vcs.time" {
					date = s.Value
				}
			}
		}
	}
}

// versionInfo returns a formatted version string.
func versionInfo() string {
	resolveVersion()
	return fmt.Sprintf("cove %s (commit %s, built %s)", version, commit, date)
}

// hostVersion returns the host binary's resolved version string.
// In dev mode, this is the git commit hash (8 chars).
func hostVersion() string {
	resolveVersion()
	if version != "dev" {
		return version
	}
	if commit != "unknown" {
		return commit
	}
	return "dev"
}
