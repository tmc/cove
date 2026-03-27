package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
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
	return fmt.Sprintf("vz-agent %s (commit %s, built %s, sha256:%s)", resolvedVersion, resolvedCommit, resolvedDate, selfSHA256())
}

// selfSHA256 returns the first 12 hex characters of the running binary's SHA256 hash.
func selfSHA256() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	f, err := os.Open(exe)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
