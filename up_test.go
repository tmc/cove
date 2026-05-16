package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSplitRecipes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "homebrew", []string{"homebrew"}},
		{"multi", "homebrew,openclaw", []string{"homebrew", "openclaw"}},
		{"trim spaces", " homebrew , openclaw ", []string{"homebrew", "openclaw"}},
		{"skip blanks", "homebrew,,openclaw,", []string{"homebrew", "openclaw"}},
		{"only commas", ",,,", nil},
		{"tabs and spaces", "\thomebrew\t,  golang\n", []string{"homebrew", "golang"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitRecipes(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitRecipes(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestVMAlreadyInstalledMacOS(t *testing.T) {
	dir := t.TempDir()
	if vmAlreadyInstalled(dir, false) {
		t.Fatalf("expected no install in empty dir")
	}
	// Empty file should not count as installed.
	if err := os.WriteFile(filepath.Join(dir, "disk.img"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if vmAlreadyInstalled(dir, false) {
		t.Fatalf("expected empty disk.img to not count as installed")
	}
	if err := os.WriteFile(filepath.Join(dir, "disk.img"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if !vmAlreadyInstalled(dir, false) {
		t.Fatalf("expected installed when disk.img has content")
	}
}

func TestVMAlreadyInstalledLinuxMarker(t *testing.T) {
	dir := t.TempDir()
	if vmAlreadyInstalled(dir, true) {
		t.Fatalf("expected no install in empty linux dir")
	}
	if err := os.WriteFile(linuxInstalledMarkerPath(dir), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	if !vmAlreadyInstalled(dir, true) {
		t.Fatalf("expected linux marker to count as installed")
	}
}

func TestParseUpFlagsErrorPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	restoreVMGlobals(t)

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing user for macOS",
			args:    []string{"-headless"},
			wantErr: "missing required flag: -user",
		},
		{
			name:    "bad automation backend",
			args:    []string{"-user", "u", "-password", "p", "-headless", "-automation-backend", "bogus"},
			wantErr: "automation",
		},
		{
			name:    "bad capture backend",
			args:    []string{"-user", "u", "-password", "p", "-headless", "-automation-capture-backend", "bogus"},
			wantErr: "automation",
		},
		{
			name:    "bad input backend",
			args:    []string{"-user", "u", "-password", "p", "-headless", "-automation-input-backend", "bogus"},
			wantErr: "automation",
		},
		{
			name:    "bad network mode",
			args:    []string{"-user", "u", "-password", "p", "-headless", "-network", "bogus"},
			wantErr: "network",
		},
		{
			name:    "missing setup script",
			args:    []string{"-user", "u", "-password", "p", "-headless", "-setup-script", filepath.Join(home, "missing.sh")},
			wantErr: "setup-script",
		},
		{
			name:    "short verbose trap",
			args:    []string{"-user", "u", "-password", "p", "-headless", "-v"},
			wantErr: "use -verbose",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseUpFlags(tt.args)
			if err == nil {
				t.Fatalf("parseUpFlags(%v) returned nil error, want %q", tt.args, tt.wantErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantErr)) {
				t.Errorf("parseUpFlags error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseUpFlagsVerbose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	restoreVMGlobals(t)

	cfg, err := parseUpFlags([]string{"-user", "u", "-password", "p", "-headless", "-verbose"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if !cfg.verbose {
		t.Fatal("cfg.verbose = false, want true")
	}
}

func TestParseUpFlagsHeadlessOverridesGUI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	restoreVMGlobals(t)

	cfg, err := parseUpFlags([]string{"-user", "u", "-password", "p", "-gui=true", "-headless"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if cfg.gui {
		t.Errorf("cfg.gui = true, want false (headless should override)")
	}
}

func TestParseUpFlagsLinuxDefaultsPassword(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	restoreVMGlobals(t)

	cfg, err := parseUpFlags([]string{"-linux", "-user", "alice", "-headless"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if cfg.password != "alice" {
		t.Errorf("cfg.password = %q, want %q (default = user for linux)", cfg.password, "alice")
	}
}
