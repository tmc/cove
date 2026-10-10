package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
	"golang.org/x/sys/unix"
)

func recoverDeletedWorkspaceRun(ctx context.Context, root string, guard *mutationguard.Guard, journal *taskDispositionJournal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state := journal.state
	if state.DiscardRequest == nil || state.State != "stopping" && state.State != "cleanup_unknown" && state.State != "discarded" {
		return fmt.Errorf("deleted discard recovery requires original explicit cleanup authority")
	}
	if _, err := verifyDeletedWorkspaceQuarantine(root, guard, state); err != nil {
		return err
	}
	pinned, err := workspaceCleanupPinsWithMissing(root, state, state.State == "discarded")
	if err != nil {
		return err
	}
	if pinned {
		return fmt.Errorf("discard recovery is operator or foreign-task pinned; retained")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := journal.requestDiscardRecovery(); err != nil {
		return err
	}
	state = journal.state
	if state.State != "discarded" {
		if err := journal.transition("discarded", state.Guest, state.Owned, state.TaskSucceeded); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return finalizeDiscardedWorkspaceGuestWithGuard(root, guard, journal.state, state.Generation)
}

func (j *taskDispositionJournal) requestDiscardRecovery() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return fmt.Errorf("task disposition journal is closed")
	}
	if j.state.DiscardRequest == nil || j.state.State != "stopping" && j.state.State != "cleanup_unknown" && j.state.State != "discarded" {
		return fmt.Errorf("discard recovery requires original explicit cleanup authority")
	}
	for _, actor := range []*taskDiscardRequest{j.state.DiscardRequest, j.state.RecoveryRequest} {
		if actor != nil && unix.Kill(actor.PID, 0) != unix.ESRCH {
			return fmt.Errorf("predecessor discard actor is active, reused, or cannot be observed; retained")
		}
	}
	if unix.Kill(j.state.OwnerPID, 0) != unix.ESRCH {
		return fmt.Errorf("original task coordinator is active, reused, or cannot be observed; retained")
	}
	started := processStartedAt(os.Getpid())
	if started.IsZero() {
		return fmt.Errorf("recovery actor process identity unavailable")
	}
	previous := j.state
	j.state.RecoveryRequest = &taskDiscardRequest{PID: os.Getpid(), StartedAt: started.UTC().Format(time.RFC3339Nano), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if j.state.State != "discarded" {
		j.state.State = "stopping"
	}
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
