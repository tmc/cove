package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

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
	calls []int32
}

func (r *recordingSignaler) SignalExec(_ context.Context, _ string, signal int32) error {
	r.calls = append(r.calls, signal)
	return nil
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
		if len(rec.calls) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if len(rec.calls) != 1 {
		t.Fatalf("SignalExec call count = %d, want 1", len(rec.calls))
	}
	if rec.calls[0] != int32(syscall.SIGINT) {
		t.Fatalf("SignalExec arg = %d, want %d", rec.calls[0], int32(syscall.SIGINT))
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
		if len(rec.calls) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if len(rec.calls) != 1 || rec.calls[0] != int32(syscall.SIGTERM) {
		t.Fatalf("SignalExec calls = %v, want [SIGTERM]", rec.calls)
	}
}
