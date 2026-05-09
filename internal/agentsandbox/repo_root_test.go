package agentsandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasAdapters(t *testing.T) {
	dir := t.TempDir()
	if hasAdapters(dir) {
		t.Fatalf("hasAdapters(%q) = true on empty dir, want false", dir)
	}
	if err := os.Mkdir(filepath.Join(dir, "adapters"), 0o755); err != nil {
		t.Fatalf("mkdir adapters: %v", err)
	}
	if !hasAdapters(dir) {
		t.Fatalf("hasAdapters(%q) = false after creating adapters dir, want true", dir)
	}
}

func TestRepoRootOrCWD(t *testing.T) {
	t.Run("explicit root short-circuits", func(t *testing.T) {
		if got := repoRootOrCWD("/explicit/path"); got != "/explicit/path" {
			t.Fatalf("repoRootOrCWD(explicit) = %q, want pass-through", got)
		}
	})

	t.Run("ascends to ancestor with adapters", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "adapters"), 0o755); err != nil {
			t.Fatalf("mkdir adapters: %v", err)
		}
		nested := filepath.Join(root, "a", "b", "c")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("mkdir nested: %v", err)
		}
		t.Chdir(nested)
		got := repoRootOrCWD("")
		// macOS may prepend /private to t.TempDir() paths in os.Getwd output;
		// resolve symlinks for both sides before comparing.
		gotResolved, _ := filepath.EvalSymlinks(got)
		wantResolved, _ := filepath.EvalSymlinks(root)
		if gotResolved != wantResolved {
			t.Fatalf("repoRootOrCWD = %q, want %q", gotResolved, wantResolved)
		}
	})

	t.Run("cwd has adapters", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "adapters"), 0o755); err != nil {
			t.Fatalf("mkdir adapters: %v", err)
		}
		t.Chdir(root)
		got := repoRootOrCWD("")
		gotResolved, _ := filepath.EvalSymlinks(got)
		wantResolved, _ := filepath.EvalSymlinks(root)
		if gotResolved != wantResolved {
			t.Fatalf("repoRootOrCWD = %q, want %q", gotResolved, wantResolved)
		}
	})
}

func TestPythonBinary(t *testing.T) {
	t.Run("default python3", func(t *testing.T) {
		t.Setenv("COVE_AGENT_SANDBOX_PYTHON", "")
		if got := pythonBinary(); got != "python3" {
			t.Fatalf("pythonBinary() = %q, want python3", got)
		}
	})
	t.Run("env override", func(t *testing.T) {
		t.Setenv("COVE_AGENT_SANDBOX_PYTHON", "/opt/py/bin/python")
		if got := pythonBinary(); got != "/opt/py/bin/python" {
			t.Fatalf("pythonBinary() = %q, want override", got)
		}
	})
	t.Run("env trims whitespace", func(t *testing.T) {
		t.Setenv("COVE_AGENT_SANDBOX_PYTHON", "  /opt/py  ")
		if got := pythonBinary(); got != "/opt/py" {
			t.Fatalf("pythonBinary() = %q, want trimmed", got)
		}
	})
	t.Run("blank-only env falls back", func(t *testing.T) {
		t.Setenv("COVE_AGENT_SANDBOX_PYTHON", "   ")
		if got := pythonBinary(); got != "python3" {
			t.Fatalf("pythonBinary() = %q, want python3 fallback", got)
		}
	})
}
