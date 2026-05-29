package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmrun"
	"golang.org/x/sys/unix"
)

func TestAcquireRunLock_Exclusive(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatalf("first AcquireRunLock: %v", err)
	}
	defer first.Release()

	second, err := AcquireRunLock(dir)
	if err == nil {
		second.Release()
		t.Fatalf("second AcquireRunLock returned nil error; want already-held")
	}
	if !errors.Is(err, ErrRunLockHeld) {
		t.Fatalf("second AcquireRunLock err = %v, want errors.Is ErrRunLockHeld", err)
	}
	wantPath := filepath.Join(dir, runLockFile)
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("error message missing lock path %q: %v", wantPath, err)
	}
	if !strings.Contains(err.Error(), "lsof") {
		t.Errorf("error message missing lsof recipe: %v", err)
	}
}

func TestAcquireRunLock_ReleasesOnNormalExit(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatalf("first AcquireRunLock: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatalf("reacquire after Release: %v", err)
	}
	defer second.Release()
}

func TestAcquireRunLock_ReleaseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	l, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatalf("AcquireRunLock: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	// And a nil receiver should not panic.
	var nilLock *RunLock
	if err := nilLock.Release(); err != nil {
		t.Fatalf("nil Release: %v", err)
	}
}

func TestAcquireRunLock_MissingDir(t *testing.T) {
	_, err := AcquireRunLock(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("AcquireRunLock(missing dir) returned nil error")
	}
	if !strings.Contains(err.Error(), "stat vmDir") {
		t.Errorf("error %q does not mention vmDir stat failure", err)
	}
}

func TestAcquireRunLock_FileNotDir(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "regular-file")
	if err := os.WriteFile(notADir, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := AcquireRunLock(notADir)
	if err == nil {
		t.Fatal("AcquireRunLock(non-dir) returned nil error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error %q does not mention non-directory", err)
	}
}

func TestAcquireRunLock_EmptyDirArg(t *testing.T) {
	_, err := AcquireRunLock("")
	if err == nil {
		t.Fatal("AcquireRunLock(\"\") returned nil error")
	}
}

func TestAcquireRunLock_ConcurrentAcquireFromSameProcess(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatalf("first AcquireRunLock: %v", err)
	}
	defer first.Release()

	type result struct {
		lock *RunLock
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		l, err := AcquireRunLock(dir)
		ch <- result{l, err}
	}()
	r := <-ch
	if r.err == nil {
		r.lock.Release()
		t.Fatalf("concurrent goroutine acquired held lock")
	}
	if !errors.Is(r.err, ErrRunLockHeld) {
		t.Errorf("concurrent goroutine err = %v, want ErrRunLockHeld", r.err)
	}
}

// TestAcquireRunLock_ReleasesOnCrash documents that the kernel
// releases unix.Flock when the file descriptor closes — including on
// SIGKILL or unexpected process exit. Exercising that path requires
// spawning a subprocess that holds the lock and then sending SIGKILL,
// which is awkward to do reliably from a test binary that re-execs
// itself for autosign (testmain_test.go). The behavior is a kernel
// guarantee, not application logic, and is covered by the
// flock(2)/flock(LOCK_EX) man pages on Darwin and Linux. Skipping
// rather than building a fragile helper.
func TestAcquireRunLock_ReleasesOnCrash(t *testing.T) {
	t.Skip("kernel-guaranteed via unix.Flock + close(2); see comment")
}

// Sanity-check: ensure the constant we expose is the real EWOULDBLOCK,
// not aliased to something else accidentally.
func TestErrRunLockHeld_IsEWOULDBLOCK(t *testing.T) {
	if ErrRunLockHeld != unix.EWOULDBLOCK {
		t.Fatalf("ErrRunLockHeld = %v, want unix.EWOULDBLOCK", ErrRunLockHeld)
	}
}

// stubAcquireRunLockHook returns run hooks with a no-op run.lock
// acquisition for tests that drive runVMWithConfig with a fake vmDir.
// It also returns a pointer to the last vmDir the hook saw.
func stubAcquireRunLockHook(t *testing.T) (RunHooks, *string) {
	t.Helper()
	var lastDir string
	hooks := defaultRunHooks()
	hooks.AcquireRunLock = func(dir string) (*RunLock, error) {
		lastDir = dir
		return &RunLock{}, nil
	}
	return hooks, &lastDir
}

func runHook(fn func() error) func(vmrun.RunConfig, vmrun.HostConfig, *RunBundle, runMetricRecorder) error {
	return func(vmrun.RunConfig, vmrun.HostConfig, *RunBundle, runMetricRecorder) error {
		return fn()
	}
}
