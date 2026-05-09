package runs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportMissingRun(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		fn   func() error
	}{
		{"json", func() error { return ExportJSON(&bytes.Buffer{}, root, "nope") }},
		{"gha", func() error { return ExportGHASummary(&bytes.Buffer{}, root, "nope") }},
		{"tar", func() error { return ExportTarGz(&bytes.Buffer{}, root, "nope") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil {
				t.Fatal("want error for missing run, got nil")
			}
			if !strings.Contains(err.Error(), "not found") {
				t.Fatalf("err = %v; want contains \"not found\"", err)
			}
		})
	}
}

func TestExportMalformedMetricsJSONL(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "20260509-bad")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metrics.jsonl"), []byte("{not json}\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, name := range []string{"json", "gha"} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			var err error
			if name == "json" {
				err = ExportJSON(&buf, root, "20260509-b")
			} else {
				err = ExportGHASummary(&buf, root, "20260509-b")
			}
			if err == nil {
				t.Fatal("want error for malformed metrics, got nil")
			}
			if !strings.Contains(err.Error(), "metrics") {
				t.Fatalf("err = %v; want contains \"metrics\"", err)
			}
		})
	}
}

func TestExportNilWriter(t *testing.T) {
	root := t.TempDir()
	for _, tt := range []struct {
		name string
		fn   func() error
	}{
		{"json", func() error { return ExportJSON(nil, root, "x") }},
		{"gha", func() error { return ExportGHASummary(nil, root, "x") }},
		{"tar", func() error { return ExportTarGz(nil, root, "x") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err == nil {
				t.Fatal("want error for nil writer, got nil")
			}
		})
	}
}
