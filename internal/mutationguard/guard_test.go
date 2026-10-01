package mutationguard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMutationGuardExcludesAndReopens(t *testing.T) {
	root := t.TempDir()
	guard, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(root); !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquisition: %v", err)
	}
	before, err := os.Stat(filepath.Join(root, "mutation.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(); err != nil {
		t.Fatal(err)
	}
	guard, err = Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	after, err := os.Stat(filepath.Join(root, "mutation.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("replaced stable lock inode")
	}
}

func TestMutationGuardRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "mutation.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(root); err == nil {
		t.Fatal("accepted symlink lock")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("target %q, error %v", data, err)
	}
}

func TestMutationGuardCrossProcess(t *testing.T) {
	root := t.TempDir()
	guard, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	run := func(mode string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestMutationGuardProcess$")
		cmd.Env = append(os.Environ(), "COVE_MUTATION_GUARD_ROOT="+root, "COVE_MUTATION_GUARD_MODE="+mode)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child %s: %v: %s", mode, err, out)
		}
	}
	run("busy")
	guard.Release()
	run("crash")
	guard, err = Acquire(root)
	if err != nil {
		t.Fatalf("process exit retained lock: %v", err)
	}
	guard.Release()
}

func TestMutationGuardProcess(t *testing.T) {
	root := os.Getenv("COVE_MUTATION_GUARD_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	guard, err := Acquire(root)
	if os.Getenv("COVE_MUTATION_GUARD_MODE") == "busy" {
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("acquisition error %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = guard
	os.Exit(0)
}

func TestMutationGuardCheck(t *testing.T) {
	root := t.TempDir()
	guard, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Check(root); err != nil {
		t.Fatal(err)
	}
	if err := guard.Check(t.TempDir()); err == nil {
		t.Fatal("accepted a different root")
	}
	guard.Release()
	if err := guard.Check(root); err == nil {
		t.Fatal("accepted a released guard")
	}
}

func TestMutationGuardAcquireContext(t *testing.T) {
	root := t.TempDir()
	guard, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := AcquireContext(ctx, root); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrBusy) {
		t.Fatalf("error %v", err)
	}
	guard.Release()
	acquired, err := AcquireContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	acquired.Release()
}

func TestMutationGuardRejectsLocalSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "other.lock")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.lock", filepath.Join(root, "mutation.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(root); err == nil {
		t.Fatal("accepted in-root symlink lock")
	}
}

func TestMutationGuardConcurrentInitialization(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-root")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const callers = 16
	results := make(chan error, callers)
	for range callers {
		go func() {
			guard, err := AcquireContext(ctx, root)
			if err == nil {
				err = guard.Release()
			}
			results <- err
		}()
	}
	for range callers {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}
