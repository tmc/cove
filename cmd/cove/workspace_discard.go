package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
	"golang.org/x/sys/unix"
)

func handleWorkspaceDiscard(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("workspace discard", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	run := fs.String("run", "", "finished run ID owning the stopped fork")
	yes := fs.Bool("y", false, "skip deletion confirmation")
	fs.BoolVar(yes, "yes", false, "skip deletion confirmation")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *run == "" {
		return fmt.Errorf("workspace discard requires -run ID")
	}
	if _, _, err := storagepins.ParseRef("run:" + *run); err != nil {
		return err
	}
	if !*yes {
		ok, err := confirmDeletef("Discard the owned stopped guest for run %q? This cannot be undone. [y/N] ", *run)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("aborted")
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := discardWorkspaceRun(ctx, coveRoot(), *run); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Discarded owned guest for run %s; task result preserved\n", *run)
	return nil
}

func openTaskDiscardJournal(dir string) (*taskDispositionJournal, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	directory, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	fd, err := unix.Openat(int(directory.Fd()), "task-disposition.lock", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	directory.Close()
	if err != nil {
		root.Close()
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), "task-disposition.lock")
	fail := func(err error) (*taskDispositionJournal, error) { lock.Close(); root.Close(); return nil, err }
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		return fail(fmt.Errorf("invalid task disposition lock"))
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(fmt.Errorf("task coordinator or discard actor still owns journal: %w", err))
	}
	state, err := readTaskDispositionRoot(root)
	if err != nil {
		return fail(err)
	}
	return &taskDispositionJournal{root: root, lock: lock, state: state}, nil
}

func (j *taskDispositionJournal) requestDiscard() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return fmt.Errorf("task disposition journal is closed")
	}
	switch j.state.State {
	case "retained", "interrupted", "cleanup_unknown", "stopping":
	default:
		return fmt.Errorf("workspace run is not terminal; retained")
	}
	if !j.state.Owned {
		return fmt.Errorf("workspace does not own this guest; retained")
	}
	if r := j.state.DiscardRequest; r != nil && unix.Kill(r.PID, 0) != unix.ESRCH {
		return fmt.Errorf("previous discard actor is active, reused, or cannot be observed; retained")
	}
	started := processStartedAt(os.Getpid())
	if started.IsZero() {
		return fmt.Errorf("operator process identity unavailable")
	}
	previous := j.state
	j.state.State = "stopping"
	j.state.DiscardRequest = &taskDiscardRequest{PID: os.Getpid(), StartedAt: started.UTC().Format(time.RFC3339Nano), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := verifyWorkspaceDiscardActor(j.state, j.state.Generation); err != nil {
		j.state = previous
		return err
	}
	if err := j.persist(); err != nil {
		j.state = previous
		return err
	}
	return nil
}

func discardWorkspaceRun(ctx context.Context, root, runID string) error {
	if _, _, err := storagepins.ParseRef("run:" + runID); err != nil {
		return err
	}
	dir := filepath.Join(root, "runs", runID)
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		return err
	}
	journal, err := openTaskDiscardJournal(dir)
	if err != nil {
		guard.Release()
		return err
	}
	defer journal.Close()
	if journal.state.RunID != runID {
		guard.Release()
		return fmt.Errorf("run identity differs")
	}
	err = journal.requestDiscard()
	err = errors.Join(err, guard.Release())
	if err != nil {
		return err
	}
	state := journal.state

	deps := defaultWorkspaceCleanupDeps()
	deps.CaptureRuntime = captureExitedWorkspaceRuntimeOwner
	deps.Stop = func(context.Context, string) error { return nil }
	deps.StoppedOwned = func(_ context.Context, _ string, owner workspaceRuntimeOwner) (bool, error) {
		return unix.Kill(owner.PID, 0) == unix.ESRCH && workspaceRuntimeExited(owner), nil
	}
	enableWorkspaceProductionCleanup(&deps, root)
	if err := cleanupWorkspaceGuest(ctx, state, state.Generation, deps); err != nil {
		persistErr := journal.transition("cleanup_unknown", state.Guest, state.Owned, state.TaskSucceeded)
		return errors.Join(err, persistErr)
	}
	if err := journal.transition("discarded", state.Guest, state.Owned, state.TaskSucceeded); err != nil {
		return err
	}
	return finalizeDiscardedWorkspaceGuest(journal.state, state.Generation)
}
