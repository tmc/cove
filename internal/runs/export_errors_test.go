package runs

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/metrics"
)

func TestExportMissingRun(t *testing.T) {
	tests := []struct {
		name string
		root func(t *testing.T) string
	}{
		{"empty json", func(t *testing.T) string { return t.TempDir() }},
		{"empty gha", func(t *testing.T) string { return t.TempDir() }},
		{"missing dir tar", func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := tt.root(t)
			var err error
			switch {
			case strings.Contains(tt.name, "json"):
				err = ExportJSON(&bytes.Buffer{}, root, "nope")
			case strings.Contains(tt.name, "gha"):
				err = ExportGHASummary(&bytes.Buffer{}, root, "nope")
			default:
				err = ExportTarGz(&bytes.Buffer{}, root, "nope")
			}
			if !errors.Is(err, ErrRunNotFound) {
				t.Fatalf("err = %v; want ErrRunNotFound", err)
			}
		})
	}
}

func TestExportSingleAndMultipleRuns(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		runs []string
		want string
	}{
		{"single", []string{"20260509-one"}, "20260509-one"},
		{"multiple", []string{"20260510-a", "20260510-b"}, "20260510-b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, id := range tt.runs {
				writeRun(t, root, id, []metrics.Event{event("run_complete", "ok", 1, map[string]any{"run_id": id})}, nil)
			}
			var buf bytes.Buffer
			if err := ExportJSON(&buf, root, tt.want); err != nil {
				t.Fatalf("ExportJSON: %v", err)
			}
			if !strings.Contains(buf.String(), tt.want) {
				t.Fatalf("export missing %q:\n%s", tt.want, buf.String())
			}
		})
	}
}

func TestListSkipsMalformedMetrics(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260509-good", []metrics.Event{event("run_complete", "ok", 1, nil)}, nil)
	dir := filepath.Join(root, "20260509-bad")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metrics.jsonl"), []byte("{not json}\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	got, err := List(root, Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].RunID != "20260509-good" {
		t.Fatalf("runs = %+v, want only good run", got)
	}
	if !strings.Contains(logs.String(), "skip malformed run metrics") {
		t.Fatalf("missing skip log:\n%s", logs.String())
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
