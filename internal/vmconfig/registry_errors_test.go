package vmconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnsetActiveNoLinkIsNoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Parent of CurrentLink may or may not exist; either way, with no
	// symlink in place UnsetActive must succeed via the IsNotExist
	// short-circuit rather than wrap an error.
	if err := UnsetActive(); err != nil {
		t.Fatalf("UnsetActive() with no link = %v, want nil", err)
	}
	// Idempotent: a second call is also a no-op.
	if err := UnsetActive(); err != nil {
		t.Fatalf("UnsetActive() second call = %v, want nil", err)
	}
}

func TestSetActiveRejectsInvalidVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tests := []struct {
		name      string
		setup     func(t *testing.T)
		vmName    string
		wantError bool
	}{
		{
			name:      "vm directory missing",
			setup:     func(t *testing.T) {},
			vmName:    "nonexistent",
			wantError: true,
		},
		{
			name: "vm directory exists but lacks required files",
			setup: func(t *testing.T) {
				if err := os.MkdirAll(filepath.Join(BaseDir(), "empty"), 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
			},
			vmName:    "empty",
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			err := SetActive(tt.vmName)
			if (err != nil) != tt.wantError {
				t.Fatalf("SetActive(%q) error = %v, wantError = %v", tt.vmName, err, tt.wantError)
			}
		})
	}
}

func TestListOrphansMissingBaseDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// BaseDir under a fresh HOME does not exist yet; the IsNotExist
	// branch must return (nil, nil) rather than an error.
	got, err := ListOrphans()
	if err != nil {
		t.Fatalf("ListOrphans() error = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("ListOrphans() = %#v, want nil", got)
	}
}
