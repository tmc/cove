// Package version resolves build and runtime version strings.
package version

import (
	"fmt"
	"runtime/debug"
)

// Info is the resolved build identity.
type Info struct {
	Version string
	Commit  string
	Date    string
}

// Resolve returns build identity, filling dev builds from Go build info.
func Resolve(version, commit, date string) Info {
	info := Info{
		Version: version,
		Commit:  commit,
		Date:    date,
	}
	if info.Version != "dev" {
		return info
	}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	for _, setting := range build.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 8 {
			info.Commit = setting.Value[:8]
		}
		if setting.Key == "vcs.time" {
			info.Date = setting.Value
		}
	}
	return info
}

// Format returns a user-facing version string for program.
func Format(program string, info Info) string {
	if info.Version == "dev" && info.Commit != "" && info.Commit != "unknown" {
		info.Version = info.Commit
	}
	return fmt.Sprintf("%s %s (commit %s, built %s)", program, info.Version, info.Commit, info.Date)
}

// Host returns the version string used for host/agent compatibility checks.
func Host(info Info) string {
	if info.Version != "dev" {
		return info.Version
	}
	if info.Commit != "unknown" {
		return info.Commit
	}
	return "dev"
}
