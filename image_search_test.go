package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/imagestore"
)

func TestSearchImagesByRefAndLabels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "runner-src")
	runner, _ := ParseImageRef("agentkit/linux-base:runner")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "runner-src", Ref: runner}); err != nil {
		t.Fatalf("BuildImage runner: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runner.Path(), "LABELS"), []byte("role=runner\nos=linux\n"), 0o644); err != nil {
		t.Fatalf("write labels: %v", err)
	}
	stageMacOSVMForImage(t, "desktop-src")
	desktop, _ := ParseImageRef("desktop/macos:stable")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "desktop-src", Ref: desktop}); err != nil {
		t.Fatalf("BuildImage desktop: %v", err)
	}

	results, err := SearchImages("runner")
	if err != nil {
		t.Fatalf("SearchImages: %v", err)
	}
	if len(results) != 1 || results[0].Ref != runner.String() {
		t.Fatalf("SearchImages runner = %#v, want %s", results, runner)
	}
	results, err = SearchImages("os=linux")
	if err != nil {
		t.Fatalf("SearchImages label: %v", err)
	}
	if len(results) != 1 || results[0].Ref != runner.String() {
		t.Fatalf("SearchImages label = %#v, want %s", results, runner)
	}
	if len(results[0].Labels) != 2 {
		t.Fatalf("Labels = %v, want 2 labels", results[0].Labels)
	}
}

func TestSearchImagesReturnsAllWithoutQuery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	for _, refText := range []string{"b:2", "a:1"} {
		ref, _ := ParseImageRef(refText)
		if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
			t.Fatalf("BuildImage %s: %v", refText, err)
		}
	}
	results, err := SearchImages("")
	if err != nil {
		t.Fatalf("SearchImages: %v", err)
	}
	if len(results) != 2 || results[0].Ref != "a:1" || results[1].Ref != "b:2" {
		t.Fatalf("SearchImages all = %#v, want sorted a:1,b:2", results)
	}
}

func TestSearchImagesNoMatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stageMacOSVMForImage(t, "src")
	ref, _ := ParseImageRef("alpha:v1")
	if _, err := BuildImage(BuildImageOptions{SourceVM: "src", Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	results, err := SearchImages("zzz-no-such-image")
	if err != nil {
		t.Fatalf("SearchImages: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("SearchImages no-match = %#v, want empty", results)
	}
}

func TestRunImageSearchAllowsTrailingJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := runImageSearch(imageTestEnv(), []string{"missing", "-json"}); err != nil {
		t.Fatalf("runImageSearch trailing -json: %v", err)
	}
	if got := strings.Join(moveImageSearchFlagsFirst([]string{"missing", "-json"}), " "); got != "-json missing" {
		t.Fatalf("moveImageSearchFlagsFirst = %q, want '-json missing'", got)
	}
}

func TestWriteImageSearchTextEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := writeImageSearchText(&buf, nil); err != nil {
		t.Fatalf("writeImageSearchText empty: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("No images found")) {
		t.Fatalf("empty search text = %q, want 'No images found'", buf.String())
	}
}

func TestWriteImageJSONEmptyIsArray(t *testing.T) {
	var buf bytes.Buffer
	if err := writeImageSearchJSON(&buf, nil); err != nil {
		t.Fatalf("writeImageSearchJSON empty: %v", err)
	}
	var got []ImageSearchResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal empty: %v\n%s", err, buf.String())
	}
	if len(got) != 0 {
		t.Fatalf("empty round trip = %#v", got)
	}
}

func TestWriteImageSearchJSON(t *testing.T) {
	results := []ImageSearchResult{{Ref: "base:v1", Name: "base", Tag: "v1", Labels: []string{"role=runner"}, Score: 80}}
	var buf bytes.Buffer
	if err := writeImageSearchJSON(&buf, results); err != nil {
		t.Fatalf("writeImageSearchJSON: %v", err)
	}
	var got []ImageSearchResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].Ref != "base:v1" || got[0].Labels[0] != "role=runner" {
		t.Fatalf("round trip = %#v", got)
	}
}

func TestWriteImageListJSONEmptyIsArray(t *testing.T) {
	var buf bytes.Buffer
	if err := writeImageListJSON(&buf, nil); err != nil {
		t.Fatalf("writeImageListJSON empty: %v", err)
	}
	var got []imageListResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal empty: %v\n%s", err, buf.String())
	}
	if len(got) != 0 {
		t.Fatalf("empty round trip = %#v", got)
	}
}

func TestWriteImageListJSON(t *testing.T) {
	created := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	entries := []imagestore.Entry{{
		Ref: imagestore.Ref{Name: "base", Tag: "v1"},
		Manifest: &imagestore.Manifest{
			DiskSize:  12,
			SourceVM:  "src",
			CreatedAt: created,
		},
	}}
	var buf bytes.Buffer
	if err := writeImageListJSON(&buf, entries); err != nil {
		t.Fatalf("writeImageListJSON: %v", err)
	}
	var got []imageListResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].Ref != "base:v1" || got[0].Source != "src" || got[0].Created != "2026-05-13T12:00:00Z" {
		t.Fatalf("round trip = %#v", got)
	}
}

func TestWriteImageSearchText(t *testing.T) {
	results := []ImageSearchResult{{Ref: "base:v1", Size: 12, Created: "2026-05-05T12:00:00Z", Labels: []string{"role=runner"}}}
	var buf bytes.Buffer
	if err := writeImageSearchText(&buf, results); err != nil {
		t.Fatalf("writeImageSearchText: %v", err)
	}
	for _, want := range []string{"REF", "base:v1", "role=runner"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("search text missing %q:\n%s", want, buf.String())
		}
	}
}
