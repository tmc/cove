package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type taskDiscardRequest struct {
	PID         int    `json:"pid"`
	StartedAt   string `json:"started_at"`
	RequestedAt string `json:"requested_at"`
}

func workspaceDiscardAuthorized(state taskDisposition) bool {
	return state.Owned && (state.TaskSucceeded && state.Policy == "discard-success" || state.DiscardRequest != nil)
}

func validateTaskDiscardRequest(state taskDisposition) error {
	r := state.DiscardRequest
	if r == nil {
		if state.RecoveryRequest != nil {
			return fmt.Errorf("discard recovery requires original operator authority")
		}
		return nil
	}
	if !state.Owned || r.PID <= 0 || r.PID == state.OwnerPID {
		return fmt.Errorf("invalid operator discard identity")
	}
	if state.State != "stopping" && state.State != "discarded" && state.State != "cleanup_unknown" {
		return fmt.Errorf("operator discard requires a cleanup disposition")
	}
	for _, value := range []string{r.StartedAt, r.RequestedAt} {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || parsed.IsZero() {
			return fmt.Errorf("invalid operator discard time")
		}
	}
	if recovery := state.RecoveryRequest; recovery != nil {
		if recovery.PID <= 0 || recovery.PID == state.OwnerPID || recovery.PID == r.PID {
			return fmt.Errorf("invalid discard recovery actor")
		}
		for _, value := range []string{recovery.StartedAt, recovery.RequestedAt} {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil || parsed.IsZero() {
				return fmt.Errorf("invalid discard recovery time")
			}
		}
	}
	return nil
}

func verifyWorkspaceDiscardActor(state taskDisposition, generation string) error {
	if err := validateTaskDisposition(state, true); err != nil {
		return err
	}
	r := state.DiscardRequest
	if state.RecoveryRequest != nil {
		if unix.Kill(r.PID, 0) != unix.ESRCH {
			return fmt.Errorf("original discard actor is active, reused, or cannot be observed; retained")
		}
		r = state.RecoveryRequest
	}
	if generation != state.Generation || r == nil || r.PID != os.Getpid() {
		return fmt.Errorf("operator discard authority differs")
	}
	started, _ := time.Parse(time.RFC3339Nano, r.StartedAt)
	if !processStartedAt(r.PID).Equal(started) {
		return fmt.Errorf("operator process identity differs or is unavailable")
	}
	if err := unix.Kill(state.OwnerPID, 0); err != unix.ESRCH {
		return fmt.Errorf("original task coordinator is active, reused, or cannot be observed; retained")
	}
	return nil
}
