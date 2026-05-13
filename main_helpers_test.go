package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestShortDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"negative clamped to zero", -5 * time.Second, "0s"},
		{"zero", 0, "0s"},
		{"sub-minute rounds to seconds", 12*time.Second + 400*time.Millisecond, "12s"},
		{"sub-minute rounds up", 12*time.Second + 600*time.Millisecond, "13s"},
		{"exact minute", time.Minute, "1m00s"},
		{"minutes and seconds", 2*time.Minute + 5*time.Second, "2m05s"},
		{"just under hour", 59*time.Minute + 30*time.Second, "59m30s"},
		{"exact hour", time.Hour, "1h00m"},
		{"hours and minutes", 3*time.Hour + 7*time.Minute, "3h07m"},
		{"hours truncate seconds", 2*time.Hour + 30*time.Minute + 45*time.Second, "2h30m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortDuration(tt.in)
			if got != tt.want {
				t.Errorf("shortDuration(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFlagWasProvided(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		flagToSet string
		check     string
		want      bool
	}{
		{"flag set on cli", []string{"-name", "alpha"}, "", "name", true},
		{"flag absent", []string{}, "", "name", false},
		{"different flag set", []string{"-other", "x"}, "", "name", false},
		{"empty-string value still counts", []string{"-name", ""}, "", "name", true},
		{"flag set via fs.Set", nil, "name", "name", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.String("name", "default", "")
			fs.String("other", "", "")
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if tt.flagToSet != "" {
				if err := fs.Set(tt.flagToSet, "via-set"); err != nil {
					t.Fatalf("set: %v", err)
				}
			}
			if got := flagWasProvided(fs, tt.check); got != tt.want {
				t.Errorf("flagWasProvided(%q) = %v, want %v (args=%v)", tt.check, got, tt.want, tt.args)
			}
		})
	}
}

func TestValidateInstallMediaPaths(t *testing.T) {
	oldInstallVM, oldIPSWPath, oldISOPath := installVM, ipswPath, isoPath
	t.Cleanup(func() {
		installVM, ipswPath, isoPath = oldInstallVM, oldIPSWPath, oldISOPath
	})
	dir := t.TempDir()
	media := filepath.Join(dir, "media.iso")
	if err := os.WriteFile(media, []byte("iso"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		args    []string
		install bool
		ipsw    string
		iso     string
		err     string
	}{
		{name: "not install", args: []string{"list"}, iso: filepath.Join(dir, "missing.iso")},
		{name: "install local ipsw missing", args: []string{"install"}, ipsw: filepath.Join(dir, "missing.ipsw"), err: "ipsw path"},
		{name: "install arg local ipsw missing", args: []string{"install", "-ipsw", filepath.Join(dir, "missing.ipsw")}, err: "ipsw path"},
		{name: "install local iso missing", args: []string{"install"}, iso: filepath.Join(dir, "missing.iso"), err: "iso path"},
		{name: "install arg local iso missing", args: []string{"install", "-linux", "-iso", filepath.Join(dir, "missing.iso")}, err: "iso path"},
		{name: "up local iso missing", args: []string{"up"}, iso: filepath.Join(dir, "missing.iso"), err: "iso path"},
		{name: "legacy install flag local iso missing", install: true, iso: filepath.Join(dir, "missing.iso"), err: "iso path"},
		{name: "url paths skip stat", args: []string{"install"}, ipsw: "https://example.test/Restore.ipsw", iso: "https://example.test/install.iso"},
		{name: "existing local paths", args: []string{"install"}, ipsw: "file://" + media, iso: media},
	} {
		t.Run(tt.name, func(t *testing.T) {
			installVM, ipswPath, isoPath = tt.install, tt.ipsw, tt.iso
			err := validateInstallMediaPaths(tt.args)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("validateInstallMediaPaths error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateInstallMediaPaths: %v", err)
			}
		})
	}
}

func TestHandleListReportsMissingDiskDirectories(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := vmconfig.BaseDir()
	if err := os.MkdirAll(filepath.Join(base, "broken-vm"), 0755); err != nil {
		t.Fatalf("mkdir broken vm: %v", err)
	}

	var buf bytes.Buffer
	if err := handleListTo(&buf); err != nil {
		t.Fatalf("handleListTo: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Missing-disk VM directories hidden from the main list:",
		"broken-vm\t(no disk image found)",
		"These are filesystem cleanup entries, not fork-lineage orphans from vm tree --orphans.",
		"Remove with: cove vm delete <name>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cove tree --orphans") {
		t.Fatalf("list output kept invalid tree guidance:\n%s", out)
	}
}
