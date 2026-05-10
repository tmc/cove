package main

import (
	"sync"
	"testing"
)

func TestImageLockReleaseNilSafe(t *testing.T) {
	var lock *ImageLock
	if err := lock.Release(); err != nil {
		t.Fatalf("nil receiver Release: %v, want nil", err)
	}
	lock2 := &ImageLock{}
	if err := lock2.Release(); err != nil {
		t.Fatalf("zero-value Release: %v, want nil", err)
	}
	if err := lock2.Release(); err != nil {
		t.Fatalf("second Release on zero-value: %v, want nil (idempotent)", err)
	}
}

// TestImageLock_Basic exercises acquire/release.
func TestImageLock_Basic(t *testing.T) {
	gcTestSetup(t)
	ref := stageUnreferencedImage(t, "src-basic", "test/basic:v1")
	lock, err := AcquireImageLock(ref.Path())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := TryAcquireImageLock(ref.Path()); err == nil {
		t.Fatalf("second acquire should fail")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	lock2, err := TryAcquireImageLock(ref.Path())
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	lock2.Release()
}

// TestGCImages_R1_SkipsLockedImage simulates a fork-from in progress
// (lock held by MaterializeImage) and asserts the gc sweep does not
// delete that image. Without the per-image lock fix, gc would delete
// the image while MaterializeImage's clonefile is still running.
func TestGCImages_R1_SkipsLockedImage(t *testing.T) {
	gcTestSetup(t)
	ref := stageUnreferencedImage(t, "src-r1", "test/r1:v1")

	// Hold the image lock as if MaterializeImage were mid-fork.
	held, err := AcquireImageLock(ref.Path())
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}
	defer held.Release()

	res, err := GCImages(ImageGCOptions{DryRun: false})
	if err != nil {
		t.Fatalf("GCImages: %v", err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("expected zero removed (image locked), got %v", res.Removed)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Ref != ref {
		t.Fatalf("expected one skipped for %s, got %+v", ref, res.Skipped)
	}
	if !ImageExists(ref) {
		t.Fatalf("image %s removed despite lock", ref)
	}
}

// TestGCImages_R2_ConcurrentFork mirrors the coved scheduler race
// shape: a fork-from materializes while gc is sweeping. The lock
// MaterializeImage now holds is released only AFTER cfg.ParentImage is
// written, so any gc that runs concurrently either skips (lock held)
// or sees the child in its recheck. This test drives both windows.
func TestGCImages_R2_ConcurrentFork(t *testing.T) {
	gcTestSetup(t)
	ref := stageUnreferencedImage(t, "src-r2", "test/r2:v1")

	var wg sync.WaitGroup
	wg.Add(2)
	var gcRes ImageGCResult
	var gcErr error
	go func() {
		defer wg.Done()
		gcRes, gcErr = GCImages(ImageGCOptions{DryRun: false})
	}()
	go func() {
		defer wg.Done()
		_, err := MaterializeImage(MaterializeImageOptions{Ref: ref, ChildName: "child-r2"})
		if err != nil {
			t.Errorf("MaterializeImage: %v", err)
		}
	}()
	wg.Wait()
	if gcErr != nil {
		t.Fatalf("GCImages: %v", gcErr)
	}
	// Either gc skipped (lock held during materialize) or gc removed
	// nothing because the recheck saw the child. The forbidden state
	// is "image removed AND child VM exists referring to it" — a torn
	// fork. Assert the image still exists OR was never live.
	if !ImageExists(ref) {
		// MaterializeImage holds the image lock through the child
		// config write, so the image must still exist when this point
		// is reached.
		t.Fatalf("image %s removed during concurrent fork (R2 regression)", ref)
	}
	_ = gcRes
}
