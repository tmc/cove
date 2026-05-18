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
		filepath.Join("/tmp/home", ".vz", "vms", "vm.covevm"),
		filepath.Join("/tmp/home", ".vz", "vm"),
		filepath.Join("/tmp/home", ".vz", "vm.covevm"),
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

func TestResolveDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	legacyPath := filepath.Join(filepath.Dir(BaseDir()), "legacy")
	if err := os.MkdirAll(legacyPath, 0755); err != nil {
		t.Fatalf("MkdirAll(legacy) error = %v", err)
	}
	if got := ResolveDir("legacy", ""); got != resolvePath(legacyPath) {
		t.Fatalf("ResolveDir(legacy) = %q, want %q", got, resolvePath(legacyPath))
	}

	explicit := filepath.Join(t.TempDir(), "explicit")
	if got := ResolveDir("", explicit); got != explicit {
		t.Fatalf("ResolveDir(explicit) = %q, want %q", got, explicit)
	}
}

func TestEnsureDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	got, err := EnsureDir("fresh", "")
	if err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	want := resolvePath(filepath.Join(BaseDir(), "fresh.covevm"))
	if got != want {
		t.Fatalf("EnsureDir() = %q, want %q", got, want)
	}
	if info, err := os.Stat(want); err != nil {
		t.Fatalf("Stat(%q) error = %v", want, err)
	} else if !info.IsDir() {
		t.Fatalf("Stat(%q).IsDir = false, want true", want)
	}
	if link, err := os.Readlink(filepath.Join(BaseDir(), "fresh")); err != nil {
		t.Fatalf("Readlink(fresh) error = %v", err)
	} else if link != want {
		t.Fatalf("fresh compatibility alias = %q, want %q", link, want)
	}
}
