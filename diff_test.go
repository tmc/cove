package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageDiffDiskLayer(t *testing.T) {
	tests := []struct {
		name   string
		old    string
		new    string
		want   string
		oldOK  bool
		newOK  bool
		change bool
	}{
		{
			name:  "identical",
			old:   "same",
			new:   "same",
			want:  "UNCHANGED",
			oldOK: true,
			newOK: true,
		},
		{
			name:   "added file",
			new:    "new",
			want:   "ADDED",
			newOK:  true,
			change: true,
		},
		{
			name:   "removed file",
			old:    "old",
			want:   "REMOVED",
			oldOK:  true,
			change: true,
		},
		{
			name:   "modified file",
			old:    "old",
			new:    "new",
			want:   "CHANGED",
			oldOK:  true,
			newOK:  true,
			change: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			a := ImageRef{Name: "a", Tag: "latest"}
			b := ImageRef{Name: "b", Tag: "latest"}
			writeTestDiffImage(t, a, tt.old, tt.oldOK)
			writeTestDiffImage(t, b, tt.new, tt.newOK)
			out, err := imageDiff(a, b)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(out.Files); got != 1 {
				t.Fatalf("len(out.Files) = %d, want 1", got)
			}
			file := out.Files[0]
			if file.Name != "disk.img" {
				t.Fatalf("file.Name = %q, want disk.img", file.Name)
			}
			if file.Status != tt.want {
				t.Fatalf("file.Status = %q, want %q", file.Status, tt.want)
			}
			if out.Changed != tt.change {
				t.Fatalf("out.Changed = %v, want %v", out.Changed, tt.change)
			}
			if (file.Old != nil) != tt.oldOK {
				t.Fatalf("old presence = %v, want %v", file.Old != nil, tt.oldOK)
			}
			if (file.New != nil) != tt.newOK {
				t.Fatalf("new presence = %v, want %v", file.New != nil, tt.newOK)
			}
			if file.Old != nil && !strings.HasPrefix(file.Old.SHA256, "sha256:") {
				t.Fatalf("old sha256 = %q, want sha256: prefix", file.Old.SHA256)
			}
			if file.New != nil && !strings.HasPrefix(file.New.SHA256, "sha256:") {
				t.Fatalf("new sha256 = %q, want sha256: prefix", file.New.SHA256)
			}
		})
	}
}

func writeTestDiffImage(t *testing.T, ref ImageRef, data string, ok bool) {
	t.Helper()
	if err := os.MkdirAll(ref.Path(), 0o755); err != nil {
		t.Fatal(err)
	}
	if !ok {
		return
	}
	if err := os.WriteFile(filepath.Join(ref.Path(), "disk.img"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
