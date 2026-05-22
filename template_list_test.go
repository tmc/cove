package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestListTemplatesPopulatedAndSorted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := vmconfig.TemplateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Stage two valid compressed templates ("zeta" and "alpha") plus one
	// invalid template ("broken" — missing required files), one fast-mode
	// template ("fast"), and a stray non-directory entry that ListTemplates
	// must skip.
	stageCompressed := func(name string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		for _, f := range TemplateFiles {
			if err := os.WriteFile(filepath.Join(p, f), []byte("x"), 0o644); err != nil {
				t.Fatalf("write %s/%s: %v", name, f, err)
			}
		}
	}
	stageCompressed("zeta")
	stageCompressed("alpha")

	// Fast template: requires the marker plus TemplateFilesFast.
	fastDir := filepath.Join(dir, "fast")
	if err := os.MkdirAll(fastDir, 0o755); err != nil {
		t.Fatalf("mkdir fast: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fastDir, TemplateMarkerFast), nil, 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}
	for _, f := range TemplateFilesFast {
		if err := os.WriteFile(filepath.Join(fastDir, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fast/%s: %v", f, err)
		}
	}

	// Invalid template: directory present but no required files.
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatalf("mkdir broken: %v", err)
	}

	// Non-directory entry (must be skipped).
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), nil, 0o644); err != nil {
		t.Fatalf("stray: %v", err)
	}

	got, err := ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}

	wantNames := []string{"alpha", "fast", "zeta"}
	if len(got) != len(wantNames) {
		t.Fatalf("got %d templates, want %d: %+v", len(got), len(wantNames), got)
	}
	for i, w := range wantNames {
		if got[i].Name != w {
			t.Errorf("[%d].Name = %q, want %q (sort or filter wrong)", i, got[i].Name, w)
		}
	}

	// Verify FastMode is propagated for the fast entry only.
	for _, info := range got {
		wantFast := info.Name == "fast"
		if info.FastMode != wantFast {
			t.Errorf("%s: FastMode = %v, want %v", info.Name, info.FastMode, wantFast)
		}
	}
}
