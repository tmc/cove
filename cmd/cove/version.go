package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "git", all...).Output()
	}
)

var versionCache struct {
	sync.Mutex
	version string
	commit  string
	date    string
	info    buildversion.Info
	valid   bool
}

func resolvedVersion() buildversion.Info {
	versionCache.Lock()
	defer versionCache.Unlock()
	if versionCache.valid && versionCache.version == version && versionCache.commit == commit && versionCache.date == date {
		return versionCache.info
	}

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
	versionCache.version = version
	versionCache.commit = commit
	versionCache.date = date
	versionCache.info = info
	versionCache.valid = true
	return info
}

func clearVersionCache() {
	versionCache.Lock()
	versionCache.valid = false
	versionCache.Unlock()
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
