package main

import (
	"sync"
	"testing"
	"time"

	"github.com/tmc/cove/internal/imagestore"
)

// TestGCImages_R1_SkipsLockedImage simulates a fork-from in progress
// (lock held by MaterializeImage) and asserts the gc sweep does not
// delete that image. Without the per-image lock fix, gc would delete
// the image while MaterializeImage's clonefile is still running.
func TestGCImages_R1_SkipsLockedImage(t *testing.T) {
	gcTestSetup(t)
	ref := stageUnreferencedImage(t, "src-r1", "test/r1:v1")

	// Hold the image lock as if MaterializeImage were mid-fork.
	held, err := imagestore.AcquireLock(ref.Path())
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

	locked := make(chan struct{})
	resume := make(chan struct{})
	oldAcquire := acquireImageLockHook
	acquireImageLockHook = func(path string) (*imagestore.Lock, error) {
		lock, err := oldAcquire(path)
		if err == nil {
			close(locked)
			<-resume
		}
		return lock, err
	}
	var release sync.Once
	finished := make(chan struct{})
	t.Cleanup(func() {
		release.Do(func() { close(resume) })
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Error("materialize worker did not stop")
		}
		acquireImageLockHook = oldAcquire
	})
	done := make(chan error, 1)
	go func() {
		defer close(finished)
		_, err := MaterializeImage(MaterializeImageOptions{Ref: ref, ChildName: "child-r2"})
		done <- err
	}()
	select {
	case <-locked:
	case err := <-done:
		t.Fatalf("materialize before lock: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("materialize did not acquire image lock")
	}
	res, err := GCImages(ImageGCOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 || !ImageExists(ref) {
		t.Fatalf("gc removed image during materialization: %+v", res)
	}
	release.Do(func() { close(resume) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("materialize: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("materialize did not finish")
	}
	res, err = GCImages(ImageGCOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 || !ImageExists(ref) {
		t.Fatalf("gc removed image after child publication: %+v", res)
	}
}
