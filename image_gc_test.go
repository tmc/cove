package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	runmetrics "github.com/tmc/vz-macos/internal/metrics"
)

// gcTestSetup stages an isolated $HOME so vmconfig.BaseDir() and
// ImagesBaseDir() never touch the developer's real ~/.vz/. This is the
// v0.1 smoke-test blocker rule (feedback_test_setenv_home_first.md):
// HOME MUST be set before any code that reads ~/.vz/.
func gcTestSetup(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// stageUnreferencedImage builds an image from a fresh source VM with no
// child VM, so VMsForkedFromImage returns empty.
func stageUnreferencedImage(t *testing.T, srcVM, refStr string) ImageRef {
	t.Helper()
	stageMacOSVMForImage(t, srcVM)
	ref, err := ParseImageRef(refStr)
	if err != nil {
		t.Fatalf("ParseImageRef(%q): %v", refStr, err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: srcVM, Ref: ref}); err != nil {
		t.Fatalf("BuildImage %s: %v", refStr, err)
	}
	return ref
}

// stageReferencedImage builds an image AND materializes a child VM so
// VMsForkedFromImage(ref) returns the child name.
func stageReferencedImage(t *testing.T, srcVM, refStr, childName string) ImageRef {
	t.Helper()
	ref := stageUnreferencedImage(t, srcVM, refStr)
	if _, err := MaterializeImage(MaterializeImageOptions{Ref: ref, ChildName: childName}); err != nil {
		t.Fatalf("MaterializeImage %s -> %s: %v", refStr, childName, err)
	}
	return ref
}

// backdateImage rewrites manifest.CreatedAt to the given time so the
// -older-than filter has a deterministic age to compare against.
func backdateImage(t *testing.T, ref ImageRef, when time.Time) {
	t.Helper()
	m, err := LoadImageManifest(ref)
	if err != nil {
		t.Fatalf("LoadImageManifest %s: %v", ref, err)
	}
	m.CreatedAt = when
	if err := writeImageManifest(ref.Path(), m); err != nil {
		t.Fatalf("writeImageManifest %s: %v", ref, err)
	}
}

func TestGCImages_NoCandidates(t *testing.T) {
	gcTestSetup(t)
	stageReferencedImage(t, "src1", "alpha:1", "child-1")
	stageReferencedImage(t, "src2", "beta:1", "child-2")

	res, err := GCImages(ImageGCOptions{})
	if err != nil {
		t.Fatalf("GCImages: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Errorf("Removed = %v, want empty", res.Removed)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("Skipped len = %d, want 2", len(res.Skipped))
	}
	for _, sk := range res.Skipped {
		if sk.Reason == "" {
			t.Errorf("Skipped[%s] has empty reason", sk.Ref)
		}
	}
}

func TestGCImages_RemovesUnreferenced(t *testing.T) {
	gcTestSetup(t)
	keep := stageReferencedImage(t, "src-keep", "keep:1", "child-keep")
	drop1 := stageUnreferencedImage(t, "src-drop1", "drop1:1")
	drop2 := stageUnreferencedImage(t, "src-drop2", "drop2:1")

	res, err := GCImages(ImageGCOptions{})
	if err != nil {
		t.Fatalf("GCImages: %v", err)
	}
	if len(res.Removed) != 2 {
		t.Fatalf("Removed = %v, want [drop1:1 drop2:1]", res.Removed)
	}
	if ImageExists(drop1) || ImageExists(drop2) {
		t.Errorf("unreferenced images still exist: drop1=%v drop2=%v",
			ImageExists(drop1), ImageExists(drop2))
	}
	if !ImageExists(keep) {
		t.Errorf("referenced image %s was deleted", keep)
	}
}

func TestGCImages_OlderThanFilter(t *testing.T) {
	gcTestSetup(t)
	recent := stageUnreferencedImage(t, "src-recent", "recent:1")
	old := stageUnreferencedImage(t, "src-old", "old:1")
	backdateImage(t, old, time.Now().Add(-7*24*time.Hour))
	backdateImage(t, recent, time.Now().Add(-1*time.Hour))

	res, err := GCImages(ImageGCOptions{OlderThan: 24 * time.Hour})
	if err != nil {
		t.Fatalf("GCImages: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != old {
		t.Fatalf("Removed = %v, want [old:1]", res.Removed)
	}
	if !ImageExists(recent) {
		t.Errorf("recent image was deleted under -older-than 24h filter")
	}
	if ImageExists(old) {
		t.Errorf("old image was not deleted")
	}
}

func TestGCImages_CacheTTLMarker(t *testing.T) {
	gcTestSetup(t)
	recent := stageUnreferencedImage(t, "src-recent", "cache/recent:latest")
	old := stageUnreferencedImage(t, "src-old", "cache/old:latest")
	for _, ref := range []ImageRef{recent, old} {
		if err := os.WriteFile(ref.Path()+"/CACHE-TTL", []byte("168h\n"), 0o644); err != nil {
			t.Fatalf("write CACHE-TTL %s: %v", ref, err)
		}
	}
	backdateImage(t, old, time.Now().Add(-8*24*time.Hour))
	backdateImage(t, recent, time.Now().Add(-1*time.Hour))

	res, err := GCImages(ImageGCOptions{})
	if err != nil {
		t.Fatalf("GCImages: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != old {
		t.Fatalf("Removed = %v, want [cache/old:latest]", res.Removed)
	}
	if !ImageExists(recent) {
		t.Errorf("recent cache image was deleted before CACHE-TTL")
	}
	if ImageExists(old) {
		t.Errorf("old cache image was not deleted")
	}
}

func TestGCImages_CacheTTLKeepsReferencedOldImage(t *testing.T) {
	gcTestSetup(t)
	ref := stageReferencedImage(t, "src", "cache/live:latest", "child-live")
	if err := os.WriteFile(ref.Path()+"/CACHE-TTL", []byte("168h\n"), 0o644); err != nil {
		t.Fatalf("write CACHE-TTL: %v", err)
	}
	backdateImage(t, ref, time.Now().Add(-8*24*time.Hour))

	res, err := GCImages(ImageGCOptions{})
	if err != nil {
		t.Fatalf("GCImages: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %v, want empty", res.Removed)
	}
	if !ImageExists(ref) {
		t.Errorf("referenced cache image was deleted")
	}
}

func TestGCImages_DryRunNoMutation(t *testing.T) {
	gcTestSetup(t)
	ref := stageUnreferencedImage(t, "src", "drop:1")

	res, err := GCImages(ImageGCOptions{DryRun: true})
	if err != nil {
		t.Fatalf("GCImages: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != ref {
		t.Fatalf("Removed = %v, want [drop:1]", res.Removed)
	}
	if !ImageExists(ref) {
		t.Error("DryRun deleted the image; expected no mutation")
	}
}

// runImageGCWithStdin swaps os.Stdin with the given pipe content and
// restores it on cleanup. fmt.Scanln reads from os.Stdin directly so
// the swap is the only way to drive the prompt deterministically.
func runImageGCWithStdin(t *testing.T, input string, args []string) error {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	w.Close()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = prev
		r.Close()
	})
	return runImageGC(args)
}

func TestRunImageGC_AbortsOnNonYes(t *testing.T) {
	gcTestSetup(t)
	ref := stageUnreferencedImage(t, "src", "drop:1")

	if err := runImageGCWithStdin(t, "n\n", nil); err != nil {
		t.Fatalf("runImageGC: %v", err)
	}
	if !ImageExists(ref) {
		t.Error("image deleted despite 'n' answer at prompt")
	}
}

func TestRunImageGC_YesSkipsPrompt(t *testing.T) {
	gcTestSetup(t)
	ref := stageUnreferencedImage(t, "src", "drop:1")

	// Empty stdin: if -yes were ignored, fmt.Scanln would block or read
	// EOF and abort. Either way the image would survive — making the
	// post-call assertion the real signal.
	if err := runImageGCWithStdin(t, "", []string{"-yes"}); err != nil {
		t.Fatalf("runImageGC -yes: %v", err)
	}
	if ImageExists(ref) {
		t.Error("image survived runImageGC -yes")
	}
}

func TestGCImagesEmitsTelemetry(t *testing.T) {
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

	recent := stageUnreferencedImage(t, "src-recent", "recent:1")
	backdateImage(t, recent, time.Now().Add(-1*time.Hour))
	keep := stageReferencedImage(t, "src-keep", "keep:1", "child-keep")
	drop := stageUnreferencedImage(t, "src-drop", "drop:1")
	backdateImage(t, drop, time.Now().Add(-8*24*time.Hour))

	run, err := beginStandaloneMetricsRun("gc-vm", "image:cache")
	if err != nil {
		t.Fatalf("beginStandaloneMetricsRun: %v", err)
	}
	_, err = GCImages(ImageGCOptions{OlderThan: 24 * time.Hour})
	finishStandaloneMetricsRun(run)
	if err != nil {
		t.Fatalf("GCImages: %v", err)
	}

	events := readMetricEventsDetailed(t, filepath.Join(run.dir, "metrics.jsonl"))
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.EventType] = true
		switch e.EventType {
		case "image_gc_keep":
			if e.Extra["reason"] == "" || e.Extra["image_ref"] == "" {
				t.Fatalf("keep event missing fields: %#v", e)
			}
		case "image_gc_evict":
			if got := asInt64(t, e.Extra["bytes_freed"]); got <= 0 {
				t.Fatalf("evict bytes_freed = %d, want > 0: %#v", got, e)
			}
			if e.Extra["image_ref"] == "" {
				t.Fatalf("evict event missing image_ref: %#v", e)
			}
		}
	}
	if !seen["image_gc_keep"] || !seen["image_gc_evict"] {
		t.Fatalf("events = %#v, want both keep and evict", events)
	}
	if !ImageExists(keep) || !ImageExists(recent) {
		t.Fatalf("referenced/recent image unexpectedly removed")
	}
	if ImageExists(drop) {
		t.Fatalf("old image was not evicted")
	}
}

func readMetricEventsDetailed(t *testing.T, path string) []runmetrics.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []runmetrics.Event
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var e runmetrics.Event
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func asInt64(t *testing.T, v any) int64 {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		t.Fatalf("unexpected numeric type %T (%v)", v, v)
		return 0
	}
}
