package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/imagestore"
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

func TestImageDiffLinuxDiskLayer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := ImageRef{Name: "a", Tag: "latest"}
	b := ImageRef{Name: "b", Tag: "latest"}
	writeTestDiffImageOS(t, a, "Linux", "same", true)
	writeTestDiffImageOS(t, b, "Linux", "same", true)

	out, err := imageDiff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(out.Files); got != 1 {
		t.Fatalf("len(out.Files) = %d, want 1", got)
	}
	file := out.Files[0]
	if file.Name != "linux-disk.img" {
		t.Fatalf("file.Name = %q, want linux-disk.img", file.Name)
	}
	if file.Status != "UNCHANGED" {
		t.Fatalf("file.Status = %q, want UNCHANGED", file.Status)
	}
}

func TestImageDiffMissingRefFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := ImageRef{Name: "a", Tag: "latest"}
	b := ImageRef{Name: "b", Tag: "latest"}
	writeTestDiffImage(t, a, "disk", true)

	if _, err := imageDiff(a, b); err == nil {
		t.Fatal("imageDiff with missing ref-b succeeded; want error")
	} else if !strings.Contains(err.Error(), "image ref not found: b:latest") {
		t.Fatalf("imageDiff missing ref-b error = %q", err.Error())
	}
	if _, err := imageDiff(b, a); err == nil {
		t.Fatal("imageDiff with missing ref-a succeeded; want error")
	} else if !strings.Contains(err.Error(), "image ref not found: b:latest") {
		t.Fatalf("imageDiff missing ref-a error = %q", err.Error())
	}
}

func TestDiffCommandAllowsTrailingJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := ImageRef{Name: "a", Tag: "latest"}
	b := ImageRef{Name: "b", Tag: "latest"}
	writeTestDiffImage(t, a, "same", true)
	writeTestDiffImage(t, b, "same", true)

	if err := diffCommand([]string{a.String(), b.String(), "-json"}); err != nil {
		t.Fatalf("diffCommand trailing -json: %v", err)
	}
}

func TestDiffCommandMissingRefJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := ImageRef{Name: "missing-a", Tag: "latest"}
	b := ImageRef{Name: "missing-b", Tag: "latest"}

	var out bytes.Buffer
	err := captureStdoutAllowError(t, &out, func() error {
		return diffCommand([]string{a.String(), b.String(), "-json"})
	})
	if err == nil {
		t.Fatal("diffCommand missing refs succeeded; want error")
	}
	if !strings.Contains(err.Error(), "image ref not found: missing-a:latest") {
		t.Fatalf("error = %q, want missing ref", err.Error())
	}
	var got imageDiffErrorOutput
	if jsonErr := json.Unmarshal(out.Bytes(), &got); jsonErr != nil {
		t.Fatalf("unmarshal diff error JSON: %v\n%s", jsonErr, out.String())
	}
	if got.Refs != [2]string{a.String(), b.String()} {
		t.Fatalf("refs = %#v, want %s/%s", got.Refs, a, b)
	}
	if !strings.Contains(got.Error, "image ref not found: missing-a:latest") {
		t.Fatalf("json error = %q, want missing ref", got.Error)
	}
}

func TestDiffCommandMissingRefText(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := ImageRef{Name: "missing-a", Tag: "latest"}
	b := ImageRef{Name: "missing-b", Tag: "latest"}

	var out bytes.Buffer
	err := captureStdoutAllowError(t, &out, func() error {
		return diffCommand([]string{a.String(), b.String()})
	})
	if err == nil {
		t.Fatal("diffCommand missing refs succeeded; want error")
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty for non-json error", out.String())
	}
}

func TestWriteImageDiffJSON(t *testing.T) {
	out := imageDiffOutput{
		Refs:    [2]string{"a:latest", "b:latest"},
		Changed: true,
		Files: []imageDiffFile{{
			Name:   "disk.img",
			Status: "CHANGED",
			Old:    &imageDiffFileValue{Size: 3, SHA256: "sha256:aaa"},
			New:    &imageDiffFileValue{Size: 4, SHA256: "sha256:bbb"},
		}},
	}
	var buf bytes.Buffer
	if err := writeImageDiffJSON(&buf, out); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Fatalf("json output missing trailing newline")
	}
	var got imageDiffOutput
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if got.Refs != out.Refs || got.Changed != out.Changed || len(got.Files) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Files[0].Status != "CHANGED" || got.Files[0].Old.SHA256 != "sha256:aaa" {
		t.Fatalf("file mismatch: %+v", got.Files[0])
	}
}

func TestWriteImageDiffErrorJSON(t *testing.T) {
	var buf bytes.Buffer
	out := imageDiffErrorOutput{
		Refs:  [2]string{"a:latest", "b:latest"},
		Error: "diff: image ref not found: a:latest",
	}
	if err := writeImageDiffErrorJSON(&buf, out); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Fatalf("json output missing trailing newline")
	}
	var got imageDiffErrorOutput
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if got != out {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestWriteImageDiffText(t *testing.T) {
	out := imageDiffOutput{
		Refs: [2]string{"a:latest", "b:latest"},
		Files: []imageDiffFile{
			{Name: "disk.img", Status: "ADDED", New: &imageDiffFileValue{Size: 7, SHA256: "sha256:xyz"}},
			{Name: "gone.img", Status: "REMOVED", Old: &imageDiffFileValue{Size: 1, SHA256: "sha256:old"}},
		},
	}
	var buf bytes.Buffer
	if err := writeImageDiffText(&buf, out); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"a:latest", "b:latest", "disk.img", "[ADDED]", "gone.img", "[REMOVED]", "<missing>", "sha256:xyz", "7 bytes"} {
		if !strings.Contains(got, want) {
			t.Fatalf("text output missing %q in:\n%s", want, got)
		}
	}
}

func writeTestDiffImage(t *testing.T, ref ImageRef, data string, ok bool) {
	t.Helper()
	writeTestDiffImageOS(t, ref, "", data, ok)
}

func writeTestDiffImageOS(t *testing.T, ref ImageRef, osType, data string, ok bool) {
	t.Helper()
	if err := os.MkdirAll(ref.Path(), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := &imagestore.Manifest{
		SchemaVersion: 1,
		Name:          ref.Name,
		Tag:           ref.Tag,
		OSType:        osType,
	}
	if err := writeImageManifest(ref.Path(), manifest); err != nil {
		t.Fatal(err)
	}
	if !ok {
		return
	}
	if err := os.WriteFile(filepath.Join(ref.Path(), imageLayoutDiskFile(osType)), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureStdoutAllowError(t *testing.T, dst *bytes.Buffer, fn func() error) error {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(dst, r)
		done <- err
	}()
	fnErr := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return fnErr
}
