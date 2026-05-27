package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/vmrun"
)

func TestBootSessionModeString(t *testing.T) {
	tests := []struct {
		mode bootSessionMode
		want string
	}{
		{bootSessionModeNormal, "normal"},
		{bootSessionModeRecovery, "recovery"},
		{bootSessionModePrivateStart, "private-start"},
		{bootSessionMode(99), "normal"},
	}
	for _, tt := range tests {
		if got := bootSessionModeString(tt.mode); got != tt.want {
			t.Errorf("bootSessionModeString(%d) = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestNormalizeBootSessionMode(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "normal"},
		{"normal", "normal"},
		{"recovery", "recovery"},
		{"private-start", "private-start"},
		{"unknown-passthrough", "unknown-passthrough"},
	}
	for _, tt := range tests {
		if got := normalizeBootSessionMode(tt.in); got != tt.want {
			t.Errorf("normalizeBootSessionMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHasSuspendState(t *testing.T) {
	oldVMDir := vmDir
	t.Cleanup(func() { vmDir = oldVMDir })

	tests := []struct {
		name    string
		writeFn func(t *testing.T, path string)
		want    bool
		// wantRemoved is true when hasSuspendState should treat the file as
		// corrupt and remove it as a side effect.
		wantRemoved bool
	}{
		{
			name:    "missing",
			writeFn: func(t *testing.T, path string) {},
			want:    false,
		},
		{
			name: "empty",
			writeFn: func(t *testing.T, path string) {
				if err := os.WriteFile(path, nil, 0644); err != nil {
					t.Fatal(err)
				}
			},
			want:        false,
			wantRemoved: true,
		},
		{
			name: "valid",
			writeFn: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("suspend-state-bytes-payload"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := suspendStatePathForVM(dir)
			tt.writeFn(t, path)
			if got := hasSuspendStateForVM(dir); got != tt.want {
				t.Fatalf("hasSuspendStateForVM() = %v, want %v", got, tt.want)
			}
			if tt.wantRemoved {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("expected suspend state removed, stat err = %v", err)
				}
			}
		})
	}
}

func TestMoveAsideSuspendState(t *testing.T) {
	dir := t.TempDir()
	statePath := suspendStatePathForVM(dir)
	cfgPath := suspendConfigPathForVM(dir)
	if err := os.WriteFile(statePath, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	moveAsideSuspendStateForVM(dir, "test reason")

	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("expected suspend state moved aside, stat err = %v", err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("expected suspend config removed, stat err = %v", err)
	}

	matches, err := filepath.Glob(statePath + ".broken-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one .broken-<ts> backup, got %v", matches)
	}

	// Second call when no state exists must be a clean no-op.
	moveAsideSuspendStateForVM(dir, "no-state")
}

func TestShouldRunGUIAutomationForRun(t *testing.T) {
	dir := t.TempDir()
	target := vmSelection{Directory: dir, Name: "t"}
	marker := target.injectSucceededMarker()

	tests := []struct {
		name      string
		strategy  string
		install   bool
		preMarker bool
		want      bool
	}{
		{name: "gui always true", strategy: "gui", want: true},
		{name: "auto without marker", strategy: "auto", want: true},
		{name: "auto with marker", strategy: "auto", preMarker: true, want: false},
		{name: "disk during install", strategy: "disk", install: true, want: false},
		{name: "disk during run upgrades to gui", strategy: "disk", install: false, want: true},
		{name: "empty strategy", strategy: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Remove(marker)
			if tt.preMarker {
				if err := os.WriteFile(marker, nil, 0644); err != nil {
					t.Fatal(err)
				}
			}
			rc := vmrun.RunConfig{
				ProvisionStrategy: tt.strategy,
				InstallVM:         tt.install,
			}
			if got := shouldRunGUIAutomationForRun(target, rc); got != tt.want {
				t.Fatalf("shouldRunGUIAutomationForRun() = %v, want %v", got, tt.want)
			}
		})
	}
}
