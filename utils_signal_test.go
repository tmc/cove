package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// TestReclaimMainSignalsRoundTrip exercises the SIGINT detach + reclaim
// path used by runLinuxShellSession. It mimics setupSignalHandler's
// channel registration without invoking the real handler (which would
// call os.Exit), then verifies that signal.Reset removes the main
// channel from the SIGINT delivery set and reclaimMainSignals re-adds it.
func TestReclaimMainSignalsRoundTrip(t *testing.T) {
	prevMain := mainSigCh
	t.Cleanup(func() {
		signal.Reset(syscall.SIGINT)
		mainSigCh = prevMain
		if mainSigCh != nil {
			signal.Notify(mainSigCh, syscall.SIGINT)
		}
	})

	mainCh := make(chan os.Signal, 1)
	mainSigCh = mainCh
	signal.Notify(mainCh, syscall.SIGINT)

	wrapperCh := make(chan os.Signal, 1)
	signal.Reset(syscall.SIGINT)
	signal.Notify(wrapperCh, syscall.SIGINT)
	t.Cleanup(func() { signal.Stop(wrapperCh) })

	// First raise: only the wrapper should see it. The main channel was
	// detached by signal.Reset before its re-Notify in reclaimMainSignals.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("syscall.Kill SIGINT: %v", err)
	}
	select {
	case <-wrapperCh:
	case <-time.After(time.Second):
		t.Fatal("wrapperCh did not receive SIGINT")
	}
	select {
	case sig := <-mainCh:
		t.Fatalf("mainCh unexpectedly received %v before reclaim", sig)
	case <-time.After(100 * time.Millisecond):
	}

	// Reclaim: re-attach SIGINT to the main channel. Wrapper still sees
	// SIGINT too (Notify is additive), which mirrors the post-shell-exit
	// state where the wrapper is still alive briefly during teardown.
	reclaimMainSignals(syscall.SIGINT)

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("syscall.Kill SIGINT (reclaim): %v", err)
	}
	select {
	case <-mainCh:
	case <-time.After(time.Second):
		t.Fatal("mainCh did not receive SIGINT after reclaim")
	}
	// Drain the wrapper too so the channel doesn't leak the signal into
	// the next test in -count=N runs.
	select {
	case <-wrapperCh:
	case <-time.After(100 * time.Millisecond):
	}
}

// TestReclaimMainSignalsNoOpWithoutSetup verifies the helper is safe
// before setupSignalHandler has run (mainSigCh nil).
func TestReclaimMainSignalsNoOpWithoutSetup(t *testing.T) {
	prev := mainSigCh
	t.Cleanup(func() { mainSigCh = prev })
	mainSigCh = nil

	// Should not panic.
	reclaimMainSignals(syscall.SIGINT)
	reclaimMainSignals() // empty signals also no-op
}
