package sckit

import (
	"context"
	"errors"
	"image"
	"sync"
	"time"
)

// ErrNoFrame reports that a stream has not produced a frame yet.
// ScreenCaptureKit delivers frames only when window content changes,
// so a freshly started stream over a static window may stay frameless
// until the first repaint.
var ErrNoFrame = errors.New("sckit: no frame captured yet")

// ErrStreamStopped reports that the stream is no longer capturing.
var ErrStreamStopped = errors.New("sckit: stream stopped")

// Stream is a persistent window capture. It keeps the most recent frame
// in a last-frame cache so Snapshot always answers immediately even
// though SCKit only delivers frames on content change. Start a Stream
// with StartStream; all methods are safe for concurrent use.
type Stream struct {
	mu       sync.Mutex
	latest   image.Image
	latestAt time.Time
	frames   uint64
	stopped  bool
	stopErr  error

	// stopFn tears down the platform capture. Set by StartStream;
	// nil for streams that never started platform capture (tests).
	stopFn func(context.Context) error

	// keepAlive pins Objective-C delegate/output wrappers (and their
	// Go closures) for the life of the stream.
	keepAlive []any
}

// storeFrame records img as the latest frame. Called from the capture
// callback; also usable directly by tests.
func (s *Stream) storeFrame(img image.Image, at time.Time) {
	if img == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = img
	s.latestAt = at
	s.frames++
}

// Snapshot returns the most recent frame and its arrival time. It
// returns ErrNoFrame when no frame has arrived yet; a stopped stream
// still serves its cached last frame.
func (s *Stream) Snapshot() (image.Image, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latest == nil {
		if s.stopped {
			if s.stopErr != nil {
				return nil, time.Time{}, s.stopErr
			}
			return nil, time.Time{}, ErrStreamStopped
		}
		return nil, time.Time{}, ErrNoFrame
	}
	return s.latest, s.latestAt, nil
}

// Frames reports how many frames have arrived since the stream started.
func (s *Stream) Frames() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frames
}

// Stopped reports whether the stream has stopped, and the stop error
// if the platform stopped it (for example on window close or a TCC
// revocation).
func (s *Stream) Stopped() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped, s.stopErr
}

// noteStop records an asynchronous platform-side stop (SCStream
// delegate stream:didStopWithError:).
func (s *Stream) noteStop(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	s.stopErr = err
}

// Stop ends the capture. It is idempotent; the last-frame cache remains
// readable through Snapshot after Stop returns.
func (s *Stream) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	stop := s.stopFn
	s.mu.Unlock()
	if stop == nil {
		return nil
	}
	if err := stop(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.keepAlive = nil
	s.mu.Unlock()
	return nil
}
