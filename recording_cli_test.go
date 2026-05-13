package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListRecordingsFindsRunArtifacts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "20260513-rec")
	if err := os.MkdirAll(filepath.Join(dir, "screenshots"), 0755); err != nil {
		t.Fatal(err)
	}
	manifest := runManifest{RunID: "20260513-rec", VMName: "work-vm", StartedAt: "2026-05-13T23:00:00Z"}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "screenshots", "one.png"), []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := listRecordings(root, 10)
	if err != nil {
		t.Fatalf("listRecordings: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("recordings = %d, want 1", len(got))
	}
	if got[0].RunID != "20260513-rec" || got[0].VMName != "work-vm" {
		t.Fatalf("recording = %+v", got[0])
	}
	if names := artifactNames(got[0].Artifacts); !strings.Contains(names, "manifest.json") || !strings.Contains(names, "screenshots") {
		t.Fatalf("artifact names = %q", names)
	}
}

func TestRecordingExportTarIncludesMetadataAndScreenshots(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "20260513-rec")
	if err := os.MkdirAll(filepath.Join(dir, "screenshots"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"run_id":"20260513-rec"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "screenshots", "one.png"), []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeRecordingTarGz(&buf, dir); err != nil {
		t.Fatalf("writeRecordingTarGz: %v", err)
	}
	names := tarNames(t, buf.Bytes())
	for _, want := range []string{"20260513-rec/manifest.json", "20260513-rec/screenshots/one.png"} {
		if !names[want] {
			t.Fatalf("tar missing %q: %#v", want, names)
		}
	}
}

func TestRecordingNoRecordingsMessage(t *testing.T) {
	var buf bytes.Buffer
	if err := printRecordingTable(&buf, nil); err != nil {
		t.Fatalf("printRecordingTable: %v", err)
	}
	if !strings.Contains(buf.String(), "No recordings found") {
		t.Fatalf("message = %q", buf.String())
	}
}

func tarNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		names[h.Name] = true
	}
	return names
}
