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

func TestLoadUTMBundleHappyPath(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "macvm.utm")
	dataDir := filepath.Join(bundle, "Data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "disk.img"), []byte("d"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "aux.bin"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := `<dict>
<key>Backend</key><string>Apple</string>
<key>Information</key><dict><key>Name</key><string>my-mac</string></dict>
<key>System</key><dict>
  <key>Boot</key><dict><key>OperatingSystem</key><string>macOS</string></dict>
  <key>CPUCount</key><integer>4</integer>
  <key>MemorySize</key><integer>2048</integer>
  <key>MacPlatform</key><dict>
    <key>HardwareModel</key><data>aGVsbG8=</data>
    <key>MachineIdentifier</key><data>d29ybGQ=</data>
    <key>AuxiliaryStoragePath</key><string>aux.bin</string>
  </dict>
</dict>
<key>Display</key><array>
  <dict><key>WidthPixels</key><integer>1920</integer><key>HeightPixels</key><integer>1080</integer></dict>
</array>
<key>Network</key><array>
  <dict><key>MacAddress</key><string>aa:bb:cc:dd:ee:ff</string></dict>
</array>
<key>Drive</key><array>
  <dict><key>ImageName</key><string>disk.img</string></dict>
</array>
</dict>`
	writePlist(t, filepath.Join(bundle, "config.plist"), body)

	cfg, err := LoadUTMBundle(bundle)
	if err != nil {
		t.Fatalf("LoadUTMBundle: %v", err)
	}
	if cfg.Name != "my-mac" {
		t.Errorf("Name = %q, want my-mac", cfg.Name)
	}
	if cfg.CPUCount != 4 {
		t.Errorf("CPUCount = %d, want 4", cfg.CPUCount)
	}
	if cfg.MemorySize != 2048*1024*1024 {
		t.Errorf("MemorySize = %d, want %d", cfg.MemorySize, 2048*1024*1024)
	}
	if cfg.DiskPath != filepath.Join(dataDir, "disk.img") {
		t.Errorf("DiskPath = %q", cfg.DiskPath)
	}
	if cfg.AuxStorage != filepath.Join(dataDir, "aux.bin") {
		t.Errorf("AuxStorage = %q", cfg.AuxStorage)
	}
	if cfg.MACAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MACAddress = %q", cfg.MACAddress)
	}
	if len(cfg.Displays) != 1 || cfg.Displays[0].Width != 1920 || cfg.Displays[0].Height != 1080 || cfg.Displays[0].PPI != 144 {
		t.Errorf("Displays = %+v", cfg.Displays)
	}
	if string(cfg.HWModel) != "hello" {
		t.Errorf("HWModel = %q, want hello", cfg.HWModel)
	}
	if string(cfg.MachineID) != "world" {
		t.Errorf("MachineID = %q, want world", cfg.MachineID)
	}
}

func TestLoadUTMBundleMissingDisk(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "missing-disk.utm")
	if err := os.MkdirAll(filepath.Join(bundle, "Data"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `<dict>
<key>Backend</key><string>Apple</string>
<key>Drive</key><array><dict><key>ImageName</key><string>nope.img</string></dict></array>
</dict>`
	writePlist(t, filepath.Join(bundle, "config.plist"), body)
	_, err := LoadUTMBundle(bundle)
	if err == nil || !strings.Contains(err.Error(), "disk image not found") {
		t.Fatalf("err = %v, want 'disk image not found'", err)
	}
}

func writePlist(t *testing.T, path, body string) {
	t.Helper()
	full := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<plist version="1.0">` + body + `</plist>`
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
}
