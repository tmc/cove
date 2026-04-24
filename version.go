package main

import buildversion "github.com/tmc/vz-macos/internal/version"

// version, commit, and date are set by goreleaser or ldflags at build time.
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc1234 -X main.date=2025-01-01T00:00:00Z"
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func resolvedVersion() buildversion.Info {
	return buildversion.Resolve(version, commit, date)
}

// versionInfo returns a formatted version string.
func versionInfo() string {
	return buildversion.Format("cove", resolvedVersion())
}

// hostVersion returns the host binary's resolved version string.
// In dev mode, this is the git commit hash (8 chars).
func hostVersion() string {
	return buildversion.Host(resolvedVersion())
}
