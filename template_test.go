package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestProvisioningSourceHash(t *testing.T) {
	h1 := ProvisioningSourceHash()
	if len(h1) != 12 {
		t.Fatalf("hash length = %d, want 12", len(h1))
	}

	// Hash should be deterministic.
	h2 := ProvisioningSourceHash()
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q != %q", h1, h2)
	}
}

func TestCheckTemplateStaleNoHash(t *testing.T) {
	dir := t.TempDir()
	stale, tmplHash, curHash := CheckTemplateStale(dir)
	if stale {
		t.Error("expected not stale when no hash file exists")
	}
	if tmplHash != "" || curHash != "" {
		t.Errorf("expected empty hashes, got tmpl=%q cur=%q", tmplHash, curHash)
	}
}

func TestCheckTemplateStaleWithHash(t *testing.T) {
	dir := t.TempDir()
	hashPath := filepath.Join(dir, TemplateHashFile)

	// Matching hash: not stale.
	if err := os.WriteFile(hashPath, []byte(ProvisioningSourceHash()), 0644); err != nil {
		t.Fatal(err)
	}
	if stale, _, _ := CheckTemplateStale(dir); stale {
		t.Error("expected not stale when hashes match")
	}

	// Mismatched hash: stale, both hashes returned.
	if err := os.WriteFile(hashPath, []byte("deadbeef0000"), 0644); err != nil {
		t.Fatal(err)
	}
	stale, tmplHash, curHash := CheckTemplateStale(dir)
	if !stale {
		t.Error("expected stale when hashes differ")
	}
	if tmplHash != "deadbeef0000" || curHash == "" {
		t.Errorf("hashes: tmpl=%q cur=%q", tmplHash, curHash)
	}
}

// TestTemplateErrorPaths covers error returns from SaveTemplate,
// CreateFromTemplate, and DeleteTemplate without exercising the
// disk-image copy/compress paths.
func TestTemplateErrorPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// SaveTemplate: source VM does not exist.
	if err := SaveTemplate("nope", "tpl"); err == nil ||
		!strings.Contains(err.Error(), "source VM not found") {
		t.Errorf("SaveTemplate missing VM: got %v, want 'source VM not found'", err)
	}

	// DeleteTemplate: template does not exist.
	if err := DeleteTemplate("ghost"); err == nil ||
		!strings.Contains(err.Error(), "template not found") {
		t.Errorf("DeleteTemplate missing: got %v, want 'template not found'", err)
	}

	// CreateFromTemplate: template does not exist.
	if err := CreateFromTemplate("ghost", "newvm"); err == nil ||
		!strings.Contains(err.Error(), "template not found or invalid") {
		t.Errorf("CreateFromTemplate missing: got %v", err)
	}

	// SaveTemplate: template already exists. Stage a minimally-valid
	// source VM and a pre-existing template directory.
	vmPath := vmconfig.Path("src")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"disk.img", "aux.img"} {
		if err := os.WriteFile(filepath.Join(vmPath, f), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	tplDir := filepath.Join(vmconfig.TemplateDir(), "dup")
	if err := os.MkdirAll(tplDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SaveTemplate("src", "dup"); err == nil ||
		!strings.Contains(err.Error(), "template already exists") {
		t.Errorf("SaveTemplate dup: got %v, want 'template already exists'", err)
	}
}

func TestListTemplatesEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d templates, want 0", len(got))
	}
	// Directory should have been created.
	if _, err := os.Stat(vmconfig.TemplateDir()); err != nil {
		t.Errorf("template dir not created: %v", err)
	}
}
