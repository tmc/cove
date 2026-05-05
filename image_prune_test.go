package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseImagePruneDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"12h", 12 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
	}
	for _, tc := range cases {
		got, err := parseImagePruneDuration(tc.in)
		if err != nil {
			t.Fatalf("parseImagePruneDuration(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("parseImagePruneDuration(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestPruneImagesOlderThan(t *testing.T) {
	gcTestSetup(t)
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	old := stageUnreferencedImage(t, "src-old", "base:old")
	recent := stageUnreferencedImage(t, "src-recent", "base:recent")
	backdateImage(t, old, now.Add(-8*24*time.Hour))
	backdateImage(t, recent, now.Add(-1*time.Hour))

	res, err := PruneImages(ImagePruneOptions{OlderThan: 7 * 24 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != old {
		t.Fatalf("Removed = %v, want [%s]", res.Removed, old)
	}
	if ImageExists(old) {
		t.Fatalf("old image still exists")
	}
	if !ImageExists(recent) {
		t.Fatalf("recent image was pruned")
	}
}

func TestPruneImagesFilter(t *testing.T) {
	gcTestSetup(t)
	old := stageUnreferencedImage(t, "src-old", "app:old-2026")
	keep := stageUnreferencedImage(t, "src-keep", "app:stable")

	res, err := PruneImages(ImagePruneOptions{Filter: "old-*"})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != old {
		t.Fatalf("Removed = %v, want [%s]", res.Removed, old)
	}
	if !ImageExists(keep) {
		t.Fatalf("non-matching image was pruned")
	}
}

func TestPruneImagesSkipsLiveForks(t *testing.T) {
	gcTestSetup(t)
	ref := stageReferencedImage(t, "src-live", "live:old", "child-live")

	res, err := PruneImages(ImagePruneOptions{Filter: "old"})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %v, want none", res.Removed)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Ref != ref {
		t.Fatalf("Skipped = %v, want %s", res.Skipped, ref)
	}
	if !ImageExists(ref) {
		t.Fatalf("referenced image was pruned")
	}
}

func TestPruneImagesDryRun(t *testing.T) {
	gcTestSetup(t)
	ref := stageUnreferencedImage(t, "src-dry", "dry:old")

	res, err := PruneImages(ImagePruneOptions{Filter: "old", DryRun: true})
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != ref {
		t.Fatalf("Removed = %v, want [%s]", res.Removed, ref)
	}
	if !ImageExists(ref) {
		t.Fatalf("dry-run pruned image")
	}
}

func TestPruneImagesEmitsTelemetry(t *testing.T) {
	gcTestSetup(t)
	runsRoot := t.TempDir()
	prev := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() {
		runsDirHook = prev
		activeMetricsMu.Lock()
		activeMetricsRun = nil
		activeMetricsMu.Unlock()
	})
	ref := stageUnreferencedImage(t, "src-drop", "drop:old")
	run, err := beginStandaloneMetricsRun("image-prune", "local")
	if err != nil {
		t.Fatalf("beginStandaloneMetricsRun: %v", err)
	}
	_, err = PruneImages(ImagePruneOptions{Filter: "old"})
	finishStandaloneMetricsRun(run)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}

	events := readMetricEventsDetailed(t, filepath.Join(run.dir, "metrics.jsonl"))
	for _, e := range events {
		if e.EventType != "image_gc_evict" {
			continue
		}
		if e.Extra["image_ref"] != ref.String() {
			t.Fatalf("image_ref = %v, want %s", e.Extra["image_ref"], ref)
		}
		if got := asInt64(t, e.Extra["bytes_freed"]); got <= 0 {
			t.Fatalf("bytes_freed = %d, want > 0", got)
		}
		return
	}
	t.Fatalf("image_gc_evict event not found: %#v", events)
}
