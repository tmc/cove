package version

import (
	"strings"
	"testing"
)

func TestFormatDevWithoutUsableCommit(t *testing.T) {
	tests := []struct {
		name string
		info Info
		want string
	}{
		{
			name: "empty commit",
			info: Info{Version: "dev", Commit: "", Date: "2026-04-23T00:00:00Z"},
			want: "cove dev (commit , built 2026-04-23T00:00:00Z)",
		},
		{
			name: "unknown commit",
			info: Info{Version: "dev", Commit: "unknown", Date: "2026-04-23T00:00:00Z"},
			want: "cove dev (commit unknown, built 2026-04-23T00:00:00Z)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Format("cove", tt.info); got != tt.want {
				t.Fatalf("Format() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveReleaseShortCircuits(t *testing.T) {
	got := Resolve("v9.9.9", "", "")
	if got.Version != "v9.9.9" {
		t.Fatalf("Resolve Version = %q, want v9.9.9", got.Version)
	}
	if got.Commit != "" || got.Date != "" {
		t.Fatalf("Resolve release filled commit/date: %#v", got)
	}
}

func TestResolveDevPopulatesFromBuildInfo(t *testing.T) {
	in := Info{Version: "dev", Commit: "seed", Date: "seed-date"}
	got := Resolve(in.Version, in.Commit, in.Date)
	if got.Version != "dev" {
		t.Fatalf("Resolve dev preserved Version = %q, want dev", got.Version)
	}
	// debug.ReadBuildInfo is available for test binaries; vcs settings
	// may be absent (untracked tree). Either the seeded values stay, or
	// they are overwritten with build-info values. Both branches are
	// valid; the only invariant is that Commit, when overwritten from
	// vcs.revision, has length 8.
	if got.Commit != "seed" && len(got.Commit) != 8 {
		t.Fatalf("Resolve dev Commit = %q (len %d), want seed or 8-char hash", got.Commit, len(got.Commit))
	}
	if got.Commit != "seed" && strings.TrimSpace(got.Commit) == "" {
		t.Fatalf("Resolve dev Commit overwritten with whitespace: %q", got.Commit)
	}
}
