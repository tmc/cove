package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

type errSignaler struct{}

func (errSignaler) SignalExec(context.Context, string, int32) error {
	return errors.New("guest dial failed")
}

func TestForwardInterruptHandlesSignalExecError(t *testing.T) {
	ch := make(chan os.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		forwardInterrupt(ctx, errSignaler{}, "exec-1", ch)
		close(done)
	}()
	ch <- syscall.SIGINT
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
}

func TestForwardInterruptReturnsOnChannelClose(t *testing.T) {
	rec := &recordingSignaler{}
	ch := make(chan os.Signal, 1)
	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		forwardInterrupt(ctx, rec, "exec-1", ch)
		close(done)
	}()
	close(ch)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forwardInterrupt did not return on channel close")
	}
}

func TestHostSignalToExecSignal(t *testing.T) {
	cases := []struct {
		name string
		in   os.Signal
		want int32
	}{
		{"SIGINT", syscall.SIGINT, int32(syscall.SIGINT)},
		{"SIGTERM", syscall.SIGTERM, int32(syscall.SIGTERM)},
		{"SIGKILL", syscall.SIGKILL, int32(syscall.SIGKILL)},
		{"unsupported SIGHUP returns 0", syscall.SIGHUP, 0},
		{"unsupported SIGUSR1 returns 0", syscall.SIGUSR1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hostSignalToExecSignal(c.in); got != c.want {
				t.Fatalf("hostSignalToExecSignal(%v) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// recordingSignaler captures SignalExec calls for assertion in tests.
type recordingSignaler struct {
	mu    sync.Mutex
	calls []int32
}

func (r *recordingSignaler) SignalExec(_ context.Context, _ string, signal int32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, signal)
	return nil
}

func (r *recordingSignaler) snapshot() []int32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int32(nil), r.calls...)
}

func TestForwardInterruptMapsSIGINTToSIGINT(t *testing.T) {
	rec := &recordingSignaler{}
	ch := make(chan os.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		forwardInterrupt(ctx, rec, "exec-1", ch)
		close(done)
	}()

	ch <- syscall.SIGINT
	// Wait for the forwarder to process the signal before cancelling.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(rec.snapshot()) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("SignalExec call count = %d, want 1", len(calls))
	}
	if calls[0] != int32(syscall.SIGINT) {
		t.Fatalf("SignalExec arg = %d, want %d", calls[0], int32(syscall.SIGINT))
	}
}

func TestForwardInterruptSkipsUnsupportedSignal(t *testing.T) {
	rec := &recordingSignaler{}
	ch := make(chan os.Signal, 2)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		forwardInterrupt(ctx, rec, "exec-1", ch)
		close(done)
	}()

	// Unsupported then supported: only the SIGTERM should produce a call.
	ch <- syscall.SIGHUP
	ch <- syscall.SIGTERM
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(rec.snapshot()) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	calls := rec.snapshot()
	if len(calls) != 1 || calls[0] != int32(syscall.SIGTERM) {
		t.Fatalf("SignalExec calls = %v, want [SIGTERM]", calls)
	}
}
