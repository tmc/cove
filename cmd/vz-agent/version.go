package main

import (
	"fmt"
	"runtime/debug"
)

// version, commit, and date are set by goreleaser or ldflags at build time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func resolvedVersionInfo() (resolvedVersion, resolvedCommit, resolvedDate string) {
	resolvedVersion = version
	resolvedCommit = commit
	resolvedDate = date

	if resolvedVersion == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					if resolvedCommit == "unknown" && len(s.Value) >= 8 {
						resolvedCommit = s.Value[:8]
					}
				case "vcs.time":
					if resolvedDate == "unknown" {
						resolvedDate = s.Value
					}
				}
			}
		}
		if resolvedCommit != "unknown" {
			resolvedVersion = resolvedCommit
		}
	}

	return resolvedVersion, resolvedCommit, resolvedDate
}

func agentVersion() string {
	resolvedVersion, _, _ := resolvedVersionInfo()
	return resolvedVersion
}

func agentVersionInfo() string {
	resolvedVersion, resolvedCommit, resolvedDate := resolvedVersionInfo()
	return fmt.Sprintf("vz-agent %s (commit %s, built %s)", resolvedVersion, resolvedCommit, resolvedDate)
}
