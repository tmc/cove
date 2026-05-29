package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	buildversion "github.com/tmc/cove/internal/version"
)

// version, commit, and date are set by goreleaser or ldflags at build time.
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc1234 -X main.date=2025-01-01T00:00:00Z"
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

var (
	versionExecutable = os.Executable
	versionStat       = os.Stat
	versionGetwd      = os.Getwd
	versionGitOutput  = func(dir string, args ...string) ([]byte, error) {
		all := append([]string{"-C", dir}, args...)
		return exec.Command("git", all...).Output()
	}
)

func resolvedVersion() buildversion.Info {
	info := buildversion.Resolve(version, commit, date)
	if info.Commit == "unknown" {
		if c := gitCommitNearExecutable(); c != "" {
			info.Commit = c
		}
	}
	if info.Date == "unknown" {
		if d := executableModTime(); d != "" {
			info.Date = d
		}
	}
	return info
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

func gitCommitNearExecutable() string {
	for _, dir := range versionProbeDirs() {
		out, err := versionGitOutput(dir, "rev-parse", "--short=12", "HEAD")
		if err == nil {
			if commit := strings.TrimSpace(string(out)); commit != "" {
				return commit
			}
		}
	}
	return ""
}

func versionProbeDirs() []string {
	var dirs []string
	if exe, err := versionExecutable(); err == nil && exe != "" {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if wd, err := versionGetwd(); err == nil && wd != "" {
		dirs = append(dirs, wd)
	}
	return dirs
}

func executableModTime() string {
	exe, err := versionExecutable()
	if err != nil || exe == "" {
		return ""
	}
	info, err := versionStat(exe)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339)
}
