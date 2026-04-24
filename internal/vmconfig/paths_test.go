package vmconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathPrefersExistingLegacyVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	legacyPath := filepath.Join(filepath.Dir(BaseDir()), "legacy")
	if err := os.MkdirAll(legacyPath, 0755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", legacyPath, err)
	}
	legacyPath = resolvePath(legacyPath)

	if got := Path("legacy"); got != legacyPath {
		t.Fatalf("Path(%q) = %q, want %q", "legacy", got, legacyPath)
	}
}

func TestPathCandidates(t *testing.T) {
	t.Setenv("HOME", "/tmp/home")
	got := PathCandidates("vm")
	want := []string{
		filepath.Join("/tmp/home", ".vz", "vms", "vm"),
		filepath.Join("/tmp/home", ".vz", "vm"),
	}
	if len(got) != len(want) {
		t.Fatalf("len(PathCandidates()) = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("PathCandidates()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIsSubdir(t *testing.T) {
	tests := []struct {
		name string
		path string
		base string
		want bool
	}{
		{name: "child", path: "/tmp/base/child", base: "/tmp/base", want: true},
		{name: "same", path: "/tmp/base", base: "/tmp/base", want: false},
		{name: "sibling prefix", path: "/tmp/base2", base: "/tmp/base", want: false},
		{name: "parent", path: "/tmp", base: "/tmp/base", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSubdir(tt.path, tt.base); got != tt.want {
				t.Fatalf("IsSubdir(%q, %q) = %v, want %v", tt.path, tt.base, got, tt.want)
			}
		})
	}
}
