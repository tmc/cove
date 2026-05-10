package coved

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImageGCSchedulerHomeReturnsConfiguredDir(t *testing.T) {
	s := &ImageGCScheduler{HomeDir: "/custom/home"}
	if got := s.home(); got != "/custom/home" {
		t.Fatalf("home() = %q, want /custom/home", got)
	}
}

func TestImageGCSchedulerHomeFallsBackToUserHome(t *testing.T) {
	s := &ImageGCScheduler{}
	want, _ := os.UserHomeDir()
	if got := s.home(); got != want {
		t.Fatalf("home() = %q, want %q", got, want)
	}
}

func TestImageGCSchedulerNowReturnsConfiguredFunc(t *testing.T) {
	fixed := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	s := &ImageGCScheduler{Now: func() time.Time { return fixed }}
	if got := s.now(); !got.Equal(fixed) {
		t.Fatalf("now() = %v, want %v", got, fixed)
	}
}

func TestImageGCSchedulerNowFallsBackToTimeNow(t *testing.T) {
	s := &ImageGCScheduler{}
	before := time.Now()
	got := s.now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("now() = %v, expected between %v and %v", got, before, after)
	}
}

func TestDirSizeSumsRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b"), []byte("worldly"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := dirSize(dir)
	want := int64(len("hello") + len("worldly"))
	if got != want {
		t.Fatalf("dirSize = %d, want %d", got, want)
	}
}

func TestDirSizeMissingDirReturnsZero(t *testing.T) {
	got := dirSize(filepath.Join(t.TempDir(), "nope"))
	if got != 0 {
		t.Fatalf("dirSize on missing dir = %d, want 0", got)
	}
}

func TestImageGCSchedulerDurationTotalMSAccumulates(t *testing.T) {
	s := &ImageGCScheduler{Now: func() time.Time { return time.Unix(0, 0) }}
	if got := s.DurationTotalMS(); got != 0 {
		t.Fatalf("initial DurationTotalMS = %d, want 0", got)
	}
	s.record(ImageGCStats{DurationMS: 250})
	s.record(ImageGCStats{DurationMS: 750})
	if got := s.DurationTotalMS(); got != 1000 {
		t.Fatalf("DurationTotalMS = %d, want 1000", got)
	}
}
