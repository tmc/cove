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

// versionInfo returns a formatted version string.
func versionInfo() string {
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
	return fmt.Sprintf("vz-macos %s (commit %s, built %s)", version, commit, date)
}
