package storagecensus

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWalkSumsAndCategorizes(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "vms", "alpha", "disk.img"), 4096)
	mustWrite(t, filepath.Join(root, "vms", "alpha", "config.json"), 256)
	mustWrite(t, filepath.Join(root, "vms", "beta", "disk.img"), 8192)
	mustWrite(t, filepath.Join(root, "images", "macos-15", "latest", "manifest.json"), 1024)
	mustWrite(t, filepath.Join(root, "runs", "abc123", "metrics.jsonl"), 64)
	// A file directly under cache/ rather than a subdir, to verify that path.
	mustWrite(t, filepath.Join(root, "cache", "blob"), 32)

	cats := []Descriptor{
		{Name: "vms", Path: filepath.Join(root, "vms")},
		{Name: "images", Path: filepath.Join(root, "images")},
		{Name: "runs", Path: filepath.Join(root, "runs")},
		{Name: "cache", Path: filepath.Join(root, "cache")},
		{Name: "build-scratch", Path: filepath.Join(root, "build-scratch")}, // missing
		{Name: "store", Path: filepath.Join(root, "store")},                 // missing
	}

	rep, err := Walk(root, cats, Options{TopN: 5, Now: time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	if rep.Root != root {
		t.Errorf("Root = %q, want %q", rep.Root, root)
	}
	wantTotal := int64(4096 + 256 + 8192 + 1024 + 64 + 32)
	if rep.UsedBytes != wantTotal {
		t.Errorf("UsedBytes = %d, want %d", rep.UsedBytes, wantTotal)
	}
	if len(rep.Categories) != len(cats) {
		t.Fatalf("Categories len = %d, want %d", len(rep.Categories), len(cats))
	}

	want := map[string]int64{
		"vms":           4096 + 256 + 8192,
		"images":        1024,
		"runs":          64,
		"cache":         32,
		"build-scratch": 0,
		"store":         0,
	}
	for _, c := range rep.Categories {
		if got := c.UsedBytes; got != want[c.Name] {
			t.Errorf("category %s UsedBytes = %d, want %d", c.Name, got, want[c.Name])
		}
	}

	// vms has two children (alpha, beta); items are top-N newest first. Both
	// were created in the same instant, so the tie-break is path-asc.
	for _, c := range rep.Categories {
		if c.Name != "vms" {
			continue
		}
		if len(c.Items) != 2 {
			t.Fatalf("vms items = %d, want 2", len(c.Items))
		}
	}
}

func TestWalkMissingRootIsNotAnError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	cats := []Descriptor{{Name: "vms", Path: filepath.Join(root, "vms")}}
	rep, err := Walk(root, cats, Options{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if rep.UsedBytes != 0 {
		t.Errorf("UsedBytes = %d, want 0", rep.UsedBytes)
	}
	if len(rep.Categories) != 1 || rep.Categories[0].UsedBytes != 0 {
		t.Errorf("category state = %+v", rep.Categories)
	}
}

func TestWalkTopNTrimsItems(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		mustWrite(t, filepath.Join(root, "runs", name, "f"), 16)
	}
	cats := []Descriptor{{Name: "runs", Path: filepath.Join(root, "runs")}}
	rep, err := Walk(root, cats, Options{TopN: 2})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got := len(rep.Categories[0].Items); got != 2 {
		t.Errorf("items len = %d, want 2", got)
	}
	// Sum stays exact even when items are trimmed.
	if got := rep.Categories[0].UsedBytes; got != 16*5 {
		t.Errorf("UsedBytes = %d, want %d", got, 16*5)
	}
}

func TestEncodeJSONIsStable(t *testing.T) {
	rep := Report{
		Root:      "/x",
		UsedBytes: 1024,
		Generated: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
		Categories: []Category{
			{Name: "vms", Path: "/x/vms", UsedBytes: 1024, Items: []Item{
				{Path: "/x/vms/a", SizeBytes: 1024, LastUsed: time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC), IsDir: true},
			}},
		},
	}
	var buf bytes.Buffer
	if err := EncodeJSON(&buf, rep); err != nil {
		t.Fatalf("EncodeJSON: %v", err)
	}
	var got Report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Root != rep.Root || got.UsedBytes != rep.UsedBytes || len(got.Categories) != 1 {
		t.Fatalf("round-trip lost data: %+v", got)
	}
}

func TestRenderHumanContainsSummaryAndCategories(t *testing.T) {
	rep := Report{
		Root:      "/x",
		UsedBytes: 5 * 1024 * 1024 * 1024,
		Categories: []Category{
			{Name: "vms", Path: "/x/vms", UsedBytes: 5 * 1024 * 1024 * 1024},
			{Name: "runs", Path: "/x/runs", UsedBytes: 0},
		},
	}
	var buf bytes.Buffer
	if err := RenderHuman(&buf, rep); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Root: /x", "Used: 5.0 GB", "vms", "runs", "0 B"} {
		if !contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func mustWrite(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}
