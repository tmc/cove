package disposable

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCloneNameRoundTrip(t *testing.T) {
	now := time.Date(2026, 3, 29, 12, 34, 56, 0, time.Local)
	got := CloneName("/tmp/research-base", now)
	want := "research-base-d-20260329-123456"
	if got != want {
		t.Fatalf("CloneName() = %q, want %q", got, want)
	}

	base, ts, ok := ParseCloneName(got)
	if !ok {
		t.Fatalf("ParseCloneName(%q) = ok=false", got)
	}
	if base != "research-base" {
		t.Fatalf("ParseCloneName(%q) base = %q, want %q", got, base, "research-base")
	}
	if !ts.Equal(now) {
		t.Fatalf("ParseCloneName(%q) time = %v, want %v", got, ts, now)
	}
}

func TestParseCloneNameRejectsAndAccepts(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantOK   bool
		wantBase string
	}{
		{name: "empty", in: "", wantOK: false},
		{name: "no -d- separator", in: "myvm-12345678-123456", wantOK: false},
		{name: "leading -d- (idx=0)", in: "-d-20240101-120000", wantOK: false},
		{name: "stamp too short", in: "vm-d-2024", wantOK: false},
		{name: "stamp wrong length", in: "vm-d-20240101-1200", wantOK: false},
		{name: "stamp unparseable", in: "vm-d-XXXXXXXX-XXXXXX", wantOK: false},
		{name: "happy path with base", in: "myvm-d-20240315-103045", wantOK: true, wantBase: "myvm"},
		{name: "happy path with whitespace base", in: "  spaced  -d-20240315-103045", wantOK: true, wantBase: "spaced"},
		{name: "blank base falls back to vm", in: "   -d-20240315-103045", wantOK: true, wantBase: "vm"},
		{name: "base with dashes is preserved", in: "my-vm-d-20240315-103045", wantOK: true, wantBase: "my-vm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, createdAt, ok := ParseCloneName(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ParseCloneName(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
			if !ok {
				if !createdAt.IsZero() {
					t.Errorf("createdAt should be zero on failure, got %v", createdAt)
				}
				return
			}
			if base != tt.wantBase {
				t.Errorf("base = %q, want %q", base, tt.wantBase)
			}
			if createdAt.IsZero() {
				t.Errorf("createdAt should not be zero on success")
			}
		})
	}
}

func TestGC(t *testing.T) {
	baseDir := t.TempDir()
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.Local)
	oldName := CloneName("research-base", now.Add(-48*time.Hour))
	newName := CloneName("research-base", now.Add(-2*time.Hour))
	activeName := CloneName("research-base", now.Add(-72*time.Hour))
	oldPath := filepath.Join(baseDir, oldName)
	newPath := filepath.Join(baseDir, newName)
	activePath := filepath.Join(baseDir, activeName)
	for _, path := range []string{oldPath, newPath, activePath} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := GC(GCOptions{
		BaseDir:   baseDir,
		OlderThan: 24 * time.Hour,
		Now: func() time.Time {
			return now
		},
		IsActive: func(path string) bool {
			return path == activePath
		},
	})
	if err != nil {
		t.Fatalf("GC() error = %v", err)
	}
	if got.Scanned != 3 {
		t.Fatalf("GC() scanned = %d, want 3", got.Scanned)
	}
	if got.SkippedAlive != 1 {
		t.Fatalf("GC() skippedAlive = %d, want 1", got.SkippedAlive)
	}
	if got.Candidates != 1 {
		t.Fatalf("GC() candidates = %d, want 1", got.Candidates)
	}
	if got.Removed != 1 {
		t.Fatalf("GC() removed = %d, want 1", got.Removed)
	}
	if len(got.Paths) != 1 || got.Paths[0] != oldPath {
		t.Fatalf("GC() paths = %#v, want [%q]", got.Paths, oldPath)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old disposable clone still exists: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("young disposable clone missing: %v", err)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active disposable clone missing: %v", err)
	}
}
