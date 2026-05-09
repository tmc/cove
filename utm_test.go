package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRegistryPaths(t *testing.T) {
	dir := t.TempDir()
	good1 := filepath.Join(dir, "VMOne.utm")
	good2 := filepath.Join(dir, "VMTwo.utm")
	for _, p := range []string{good1, good2} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(dir, "Missing.utm")

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"quoted Path", `"Path" = "` + good1 + `";`, []string{good1}},
		{"unquoted Path", `Path = "` + good2 + `";`, []string{good2}},
		{"non-utm suffix ignored", `"Path" = "/tmp/x.txt";`, nil},
		{"missing on disk dropped", `"Path" = "` + missing + `";`, nil},
		{"mixed lines", `"Path" = "` + good1 + "\"\nOther = \"x\"\nPath = \"" + good2 + `";`, []string{good1, good2}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRegistryPaths(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("path[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestLoadUTMBundleErrors(t *testing.T) {
	dir := t.TempDir()

	notDir := filepath.Join(dir, "file.utm")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mkdir := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	noPlist := mkdir("noplist.utm")
	wrongBackend := mkdir("qemu.utm")
	writePlist(t, filepath.Join(wrongBackend, "config.plist"), `<dict><key>Backend</key><string>QEMU</string></dict>`)
	wrongOS := mkdir("linux.utm")
	writePlist(t, filepath.Join(wrongOS, "config.plist"), `<dict><key>Backend</key><string>Apple</string><key>System</key><dict><key>Boot</key><dict><key>OperatingSystem</key><string>Linux</string></dict></dict></dict>`)

	tests := []struct {
		name    string
		path    string
		wantSub string
	}{
		{"missing bundle", filepath.Join(dir, "nope.utm"), "bundle not found"},
		{"not a directory", notDir, "must be a directory"},
		{"no config.plist", noPlist, "parse config.plist"},
		{"non-Apple backend", wrongBackend, "only Apple backend"},
		{"non-macOS guest", wrongOS, "only macOS guests"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadUTMBundle(tt.path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func writePlist(t *testing.T, path, body string) {
	t.Helper()
	full := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<plist version="1.0">` + body + `</plist>`
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
}
