package main

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

// volumeFileInfo is a minimal os.FileInfo for stubbing hostPathStat.
type volumeFileInfo struct {
	dir bool
}

func (f volumeFileInfo) Name() string       { return "" }
func (f volumeFileInfo) Size() int64        { return 0 }
func (f volumeFileInfo) Mode() fs.FileMode  { return 0 }
func (f volumeFileInfo) ModTime() time.Time { return time.Time{} }
func (f volumeFileInfo) IsDir() bool        { return f.dir }
func (f volumeFileInfo) Sys() any           { return nil }

// stubHostPaths makes hostPathStat report each listed path as an existing
// directory (dir=true), an existing file (dir=false), or missing (absent).
func stubHostPaths(t *testing.T, dirs map[string]bool) {
	t.Helper()
	old := hostPathStat
	t.Cleanup(func() { hostPathStat = old })
	hostPathStat = func(path string) (os.FileInfo, error) {
		isDir, ok := dirs[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return volumeFileInfo{dir: isDir}, nil
	}
}

func TestValidateVolumes(t *testing.T) {
	stubHostPaths(t, map[string]bool{
		"/exists/a":   true,
		"/exists/b":   true,
		"/exists/dup": true,
		"/a/file":     false,
	})

	tests := []struct {
		name        string
		mounts      []vmconfig.VolumeMount
		wantKept    []string // host paths, in order
		wantSkipped []string // reason substrings, in order
	}{
		{
			name:     "all valid",
			mounts:   []vmconfig.VolumeMount{{HostPath: "/exists/a", Tag: "a"}, {HostPath: "/exists/b", Tag: "b"}},
			wantKept: []string{"/exists/a", "/exists/b"},
		},
		{
			name:        "missing host path skipped",
			mounts:      []vmconfig.VolumeMount{{HostPath: "/exists/a", Tag: "a"}, {HostPath: "/gone", Tag: "hf"}},
			wantKept:    []string{"/exists/a"},
			wantSkipped: []string{"host path missing: /gone"},
		},
		{
			name:        "empty host path skipped",
			mounts:      []vmconfig.VolumeMount{{HostPath: "", Tag: "x"}},
			wantSkipped: []string{"empty host path"},
		},
		{
			name:        "non-directory skipped",
			mounts:      []vmconfig.VolumeMount{{HostPath: "/a/file", Tag: "f"}},
			wantSkipped: []string{"not a directory"},
		},
		{
			name:        "duplicate explicit tag skipped",
			mounts:      []vmconfig.VolumeMount{{HostPath: "/exists/a", Tag: "dup"}, {HostPath: "/exists/dup", Tag: "dup"}},
			wantKept:    []string{"/exists/a"},
			wantSkipped: []string{"duplicate tag: dup"},
		},
		{
			name:     "empty tags not deduplicated",
			mounts:   []vmconfig.VolumeMount{{HostPath: "/exists/a"}, {HostPath: "/exists/b"}},
			wantKept: []string{"/exists/a", "/exists/b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, skipped := validateVolumes(tt.mounts)
			gotKept := make([]string, len(kept))
			for i, m := range kept {
				gotKept[i] = m.HostPath
			}
			if strings.Join(gotKept, ",") != strings.Join(tt.wantKept, ",") {
				t.Fatalf("kept = %v, want %v", gotKept, tt.wantKept)
			}
			if len(skipped) != len(tt.wantSkipped) {
				t.Fatalf("skipped = %d %+v, want %d", len(skipped), skipped, len(tt.wantSkipped))
			}
			for i, want := range tt.wantSkipped {
				if !strings.Contains(skipped[i].Reason, want) {
					t.Fatalf("skipped[%d].Reason = %q, want contains %q", i, skipped[i].Reason, want)
				}
			}
		})
	}
}

func TestHostDoctorVolumeSharesCheck(t *testing.T) {
	// One VM with a good volume and a missing-path volume; one clean VM.
	goodDir := t.TempDir()
	badVM := t.TempDir()
	cleanVM := t.TempDir()

	if err := vmconfig.SetVolumes(badVM, []vmconfig.VolumeMount{
		{HostPath: goodDir, Tag: "tmc"},
		{HostPath: "/definitely/not/here", Tag: "huggingface", ReadOnly: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := vmconfig.SetVolumes(cleanVM, []vmconfig.VolumeMount{
		{HostPath: goodDir, Tag: "tmc"},
	}); err != nil {
		t.Fatal(err)
	}

	oldList := vmProcessListVMs
	t.Cleanup(func() { vmProcessListVMs = oldList })
	vmProcessListVMs = func() ([]vmconfig.Info, error) {
		return []vmconfig.Info{
			{Name: "mlx-lm", Path: badVM},
			{Name: "clean", Path: cleanVM},
		}, nil
	}

	check := hostDoctorVolumeSharesCheck()
	if check.Name != "volume-shares" {
		t.Fatalf("name = %q", check.Name)
	}
	if check.Status != "warn" {
		t.Fatalf("status = %q, want warn (%q)", check.Status, check.Message)
	}
	if !strings.Contains(check.Message, "mlx-lm") || !strings.Contains(check.Message, "/definitely/not/here") {
		t.Fatalf("message missing offending volume: %q", check.Message)
	}
	if strings.Contains(check.Message, "clean:") {
		t.Fatalf("clean VM should not be flagged: %q", check.Message)
	}
}

func TestHostDoctorVolumeSharesCheckPass(t *testing.T) {
	goodDir := t.TempDir()
	vm := t.TempDir()
	if err := vmconfig.SetVolumes(vm, []vmconfig.VolumeMount{{HostPath: goodDir, Tag: "tmc"}}); err != nil {
		t.Fatal(err)
	}
	oldList := vmProcessListVMs
	t.Cleanup(func() { vmProcessListVMs = oldList })
	vmProcessListVMs = func() ([]vmconfig.Info, error) {
		return []vmconfig.Info{{Name: "clean", Path: vm}}, nil
	}
	check := hostDoctorVolumeSharesCheck()
	if check.Status != "pass" {
		t.Fatalf("status = %q, want pass (%q)", check.Status, check.Message)
	}
}
