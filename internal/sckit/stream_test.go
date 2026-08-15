package sckit

import (
	"context"
	"errors"
	"image"
	"sync"
	"testing"
	"time"
)

// These tests exercise the last-frame cache and lifecycle of Stream
// without touching ScreenCaptureKit or TCC. The live SCStream path is
// covered by stream_live_test.go under the sckit_live tag.

func TestStreamSnapshotEmpty(t *testing.T) {
	s := &Stream{}
	if _, _, err := s.Snapshot(); !errors.Is(err, ErrNoFrame) {
		t.Fatalf("Snapshot on empty stream = %v, want ErrNoFrame", err)
	}
	if got := s.Frames(); got != 0 {
		t.Fatalf("Frames = %d, want 0", got)
	}
}

func TestStreamCachesLatestFrame(t *testing.T) {
	s := &Stream{}
	first := image.NewRGBA(image.Rect(0, 0, 1, 1))
	second := image.NewRGBA(image.Rect(0, 0, 2, 2))
	t0 := time.Unix(100, 0)
	t1 := time.Unix(200, 0)

	s.storeFrame(first, t0)
	s.storeFrame(second, t1)

	img, at, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if img != second {
		t.Fatal("Snapshot did not return the latest frame")
	}
	if !at.Equal(t1) {
		t.Fatalf("Snapshot time = %v, want %v", at, t1)
	}
	if got := s.Frames(); got != 2 {
		t.Fatalf("Frames = %d, want 2", got)
	}
}

func TestStreamStoreFrameIgnoresNil(t *testing.T) {
	s := &Stream{}
	s.storeFrame(nil, time.Now())
	if got := s.Frames(); got != 0 {
		t.Fatalf("Frames after nil store = %d, want 0", got)
	}
}

func TestStreamStopIdempotentAndKeepsCache(t *testing.T) {
	var stops int
	s := &Stream{stopFn: func(context.Context) error {
		stops++
		return nil
	}}
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	s.storeFrame(frame, time.Now())

	ctx := context.Background()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if stops != 1 {
		t.Fatalf("stopFn called %d times, want 1", stops)
	}
	if stopped, err := s.Stopped(); !stopped || err != nil {
		t.Fatalf("Stopped = %v, %v, want true, nil", stopped, err)
	}
	if img, _, err := s.Snapshot(); err != nil || img != frame {
		t.Fatalf("Snapshot after Stop = %v, %v; want cached frame", img, err)
	}
}

func TestStreamSnapshotAfterStopWithoutFrames(t *testing.T) {
	s := &Stream{}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, _, err := s.Snapshot(); !errors.Is(err, ErrStreamStopped) {
		t.Fatalf("Snapshot = %v, want ErrStreamStopped", err)
	}
}

func TestStreamNoteStopReportsError(t *testing.T) {
	s := &Stream{}
	cause := errors.New("window closed")
	s.noteStop(cause)
	if _, _, err := s.Snapshot(); !errors.Is(err, cause) {
		t.Fatalf("Snapshot = %v, want %v", err, cause)
	}
	// A later local Stop stays a no-op success.
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after noteStop: %v", err)
	}
}

func TestStreamConcurrentAccess(t *testing.T) {
	s := &Stream{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.storeFrame(image.NewRGBA(image.Rect(0, 0, 1, 1)), time.Now())
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.Snapshot()
				s.Frames()
			}
		}()
	}
	wg.Wait()
	if got := s.Frames(); got != 400 {
		t.Fatalf("Frames = %d, want 400", got)
	}
}
