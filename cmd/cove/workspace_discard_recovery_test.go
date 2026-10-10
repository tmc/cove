package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
)

func deletedDiscardRecoveryFixture(t *testing.T) (string, taskDisposition, workspaceQuarantineReceipt) {
	t.Helper()
	root, guard, state, targets := workspacePinFixture(t)
	t.Setenv(vmconfig.StateDirEnv, root)
	state.OwnerPID = exitedDiscardPID(t)
	state.State = "stopping"
	state.Policy = "retain"
	state.TaskSucceeded = false
	state.DiscardRequest = &taskDiscardRequest{PID: exitedDiscardPID(t), StartedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), RequestedAt: time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)}
	writeWorkspacePinDisposition(t, targets[1].Identity.Path, state)
	if err := updateWorkspaceTaskPins(root, guard, state, state.Generation, targets, false, workspacePinTestOwner, nil); err != nil {
		t.Fatal(err)
	}
	receipt, err := quarantineWorkspaceGuest(root, state, state.Generation, guard)
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteWorkspaceQuarantine(root, receipt, state.Generation, guard); err != nil {
		t.Fatal(err)
	}
	receipt.Phase = "deleted"
	if err := os.WriteFile(filepath.Join(targets[1].Identity.Path, "task-disposition.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	guard.Release()
	return root, state, receipt
}

func rewriteRecoveryReceipt(t *testing.T, root string, receipt workspaceQuarantineReceipt) {
	t.Helper()
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	parent, _, _, err := openWorkspaceQuarantineParent(root, guard)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	q, err := openWorkspaceQuarantine(parent, false)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := writeWorkspaceQuarantineReceipt(q, receipt, true); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceDeletedDiscardRecovery(t *testing.T) {
	for _, initial := range []string{"stopping", "cleanup_unknown", "discarded"} {
		t.Run(initial, func(t *testing.T) {
			root, state, receipt := deletedDiscardRecoveryFixture(t)
			state.State = initial
			run := filepath.Join(root, "runs", state.RunID)
			writeWorkspacePinDisposition(t, run, state)
			if err := discardWorkspaceRun(context.Background(), root, state.RunID); err != nil {
				t.Fatal(err)
			}
			got, err := readTaskDisposition(run)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != "discarded" || got.TaskSucceeded || got.Policy != "retain" || got.RecoveryRequest == nil || got.RecoveryRequest.PID != os.Getpid() {
				t.Fatalf("invalid recovered state %+v", got)
			}
			if !reflect.DeepEqual(got.DiscardRequest, state.DiscardRequest) || got.OwnerPID != state.OwnerPID || got.Generation != state.Generation || got.AttemptID != state.AttemptID || !reflect.DeepEqual(got.SourceGuest, state.SourceGuest) || !reflect.DeepEqual(got.Guest, state.Guest) {
				t.Fatal("recovery rebound original ownership")
			}
			pins, err := storagepins.Load(root)
			if err != nil || len(pins.TaskPins()) != 0 {
				t.Fatalf("pins remain %v", err)
			}
			guard, err := mutationguard.Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			recoveredReceipt, err := verifyDeletedWorkspaceQuarantine(root, guard, got)
			guard.Release()
			if err != nil || !reflect.DeepEqual(recoveredReceipt, receipt) {
				t.Fatalf("receipt changed %+v %v", recoveredReceipt, err)
			}
		})
	}
}

func TestWorkspaceDeletedDiscardRecoveryRejectsUnsafeEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string, *taskDisposition, *workspaceQuarantineReceipt)
	}{
		{"moved", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			q.Phase = "moved"
			rewriteRecoveryReceipt(t, r, *q)
		}},
		{"prepared", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			q.Phase = "prepared"
			rewriteRecoveryReceipt(t, r, *q)
		}},
		{"request-mismatch", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			request := *q.Task.DiscardRequest
			request.RequestedAt = time.Now().UTC().Format(time.RFC3339Nano)
			q.Task.DiscardRequest = &request
			rewriteRecoveryReceipt(t, r, *q)
		}},
		{"missing-receipt", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			if err := os.Remove(filepath.Join(r, "vms", workspaceQuarantineDirectory, s.Generation+".json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"coordinator-reused", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			s.OwnerPID = os.Getpid()
			s.OwnerStartedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			q.Task.OwnerPID = s.OwnerPID
			q.Task.OwnerStartedAt = s.OwnerStartedAt
			rewriteRecoveryReceipt(t, r, *q)
		}},
		{"predecessor-reused", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			request := *s.DiscardRequest
			request.PID = os.Getpid()
			request.StartedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			s.DiscardRequest = &request
			q.Task.DiscardRequest = &request
			rewriteRecoveryReceipt(t, r, *q)
		}},
		{"recovery-actor-reused", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			s.RecoveryRequest = &taskDiscardRequest{PID: os.Getpid(), StartedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		}},
		{"source-replaced", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			if err := os.Rename(s.Source, s.Source+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(s.Source, 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"guest-replaced", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			if err := os.Mkdir(s.Guest.Path, 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"quarantine-present", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			if err := os.Mkdir(filepath.Join(r, "vms", workspaceQuarantineDirectory, q.Name), 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"quarantine-parent-replaced", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			path := filepath.Join(r, "vms", workspaceQuarantineDirectory)
			if err := os.Rename(path, path+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(path+"-original", s.Generation+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, s.Generation+".json"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"operator-pin", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			guard, err := mutationguard.Acquire(r)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Release()
			if err := storagepins.UpdateWithGuard(r, guard, func(p *storagepins.File) (bool, error) { p.Add("run", s.RunID, time.Now()); return true, nil }); err != nil {
				t.Fatal(err)
			}
		}},
		{"foreign-pin", func(t *testing.T, r string, s *taskDisposition, q *workspaceQuarantineReceipt) {
			guard, err := mutationguard.Acquire(r)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Release()
			if err := storagepins.UpdateWithGuard(r, guard, func(p *storagepins.File) (bool, error) {
				pin := p.TaskPins()[0]
				pin.Owner.Generation = "ffffffffffffffffffffffffffffffff"
				return true, p.AddTask(pin)
			}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, state, receipt := deletedDiscardRecoveryFixture(t)
			tt.change(t, root, &state, &receipt)
			run := filepath.Join(root, "runs", state.RunID)
			writeWorkspacePinDisposition(t, run, state)
			before, err := os.ReadFile(filepath.Join(run, "task-disposition.json"))
			if err != nil {
				t.Fatal(err)
			}
			pinsBefore, err := storagepins.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			originalPins := pinsBefore.TaskPins()
			if err := discardWorkspaceRun(context.Background(), root, state.RunID); err == nil {
				t.Fatal("accepted unsafe recovery")
			}
			after, err := os.ReadFile(filepath.Join(run, "task-disposition.json"))
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("unsafe recovery changed journal")
			}
			pins, err := storagepins.Load(root)
			if err != nil || !reflect.DeepEqual(pins.TaskPins(), originalPins) {
				t.Fatalf("unsafe recovery changed task pins %v", err)
			}
		})
	}
}

func TestWorkspaceDeletedRecoveryPersistenceFailure(t *testing.T) {
	root, state, _ := deletedDiscardRecoveryFixture(t)
	journal, err := openTaskDiscardJournal(filepath.Join(root, "runs", state.RunID))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	before := journal.state
	journal.root.Close()
	if err := journal.requestDiscardRecovery(); err == nil {
		t.Fatal("recovery persisted through closed root")
	}
	if !reflect.DeepEqual(journal.state, before) {
		t.Fatal("failed persistence changed authority")
	}
	pins, err := storagepins.Load(root)
	if err != nil || len(pins.TaskPins()) != 2 {
		t.Fatalf("failed persistence released pins %v", err)
	}
}

func TestWorkspaceDeletedRecoveryDiscardedPersistenceFailure(t *testing.T) {
	root, state, _ := deletedDiscardRecoveryFixture(t)
	journal, err := openTaskDiscardJournal(filepath.Join(root, "runs", state.RunID))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := journal.requestDiscardRecovery(); err != nil {
		t.Fatal(err)
	}
	journal.root.Close()
	if err := journal.transition("discarded", state.Guest, true, false); err == nil {
		t.Fatal("discarded persisted through closed root")
	}
	durable, err := readTaskDisposition(filepath.Join(root, "runs", state.RunID))
	if err != nil {
		t.Fatal(err)
	}
	if durable.State != "stopping" || durable.TaskSucceeded {
		t.Fatalf("invalid failed persistence state %+v", durable)
	}
	pins, err := storagepins.Load(root)
	if err != nil || len(pins.TaskPins()) != 2 {
		t.Fatalf("failed discarded persistence released pins %v", err)
	}
}

func TestWorkspaceDeletedRecoveryActorRefusal(t *testing.T) {
	for _, actor := range []string{"coordinator", "original", "recovery"} {
		t.Run(actor, func(t *testing.T) {
			root, state, _ := deletedDiscardRecoveryFixture(t)
			journal, err := openTaskDiscardJournal(filepath.Join(root, "runs", state.RunID))
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			reused := &taskDiscardRequest{PID: os.Getpid(), StartedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			switch actor {
			case "coordinator":
				journal.state.OwnerPID = os.Getpid()
			case "original":
				journal.state.DiscardRequest = reused
			case "recovery":
				journal.state.RecoveryRequest = reused
			}
			before := journal.state
			if err := journal.requestDiscardRecovery(); err == nil {
				t.Fatal("accepted occupied predecessor PID with different start time")
			}
			if !reflect.DeepEqual(journal.state, before) {
				t.Fatal("actor refusal changed authority")
			}
		})
	}
}

func TestWorkspaceDeletedRecoveryCancelled(t *testing.T) {
	root, state, _ := deletedDiscardRecoveryFixture(t)
	before, err := readTaskDisposition(filepath.Join(root, "runs", state.RunID))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := discardWorkspaceRun(ctx, root, state.RunID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	after, err := readTaskDisposition(filepath.Join(root, "runs", state.RunID))
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("cancelled recovery changed state %v", err)
	}
	pins, err := storagepins.Load(root)
	if err != nil || len(pins.TaskPins()) != 2 {
		t.Fatalf("cancelled recovery released pins %v", err)
	}
}

func TestWorkspaceDeletedRecoveryFinalizationFailure(t *testing.T) {
	root, state, receipt := deletedDiscardRecoveryFixture(t)
	journal, err := openTaskDiscardJournal(filepath.Join(root, "runs", state.RunID))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := journal.requestDiscardRecovery(); err != nil {
		t.Fatal(err)
	}
	if err := journal.transition("discarded", state.Guest, true, false); err != nil {
		t.Fatal(err)
	}
	receipt.Task.Policy = "discard-success"
	rewriteRecoveryReceipt(t, root, receipt)
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	if err := finalizeDiscardedWorkspaceGuestWithGuard(root, guard, journal.state, state.Generation); err == nil {
		t.Fatal("released pins after deletion authority changed")
	}
	pins, err := storagepins.Load(root)
	if err != nil || len(pins.TaskPins()) != 2 {
		t.Fatalf("failed finalization released pins %v", err)
	}
	durable, err := readTaskDisposition(filepath.Join(root, "runs", state.RunID))
	if err != nil || durable.State != "discarded" || durable.TaskSucceeded || durable.Policy != "retain" {
		t.Fatalf("failed finalization changed original result %+v %v", durable, err)
	}
}

func TestWorkspaceDeletedRecoveryMissingPins(t *testing.T) {
	for _, tt := range []struct {
		name    string
		missing int
		want    bool
	}{{"partial", 1, false}, {"already-released", 2, true}} {
		t.Run(tt.name, func(t *testing.T) {
			missing := tt.missing
			root, state, _ := deletedDiscardRecoveryFixture(t)
			state.State = "discarded"
			run := filepath.Join(root, "runs", state.RunID)
			writeWorkspacePinDisposition(t, run, state)
			guard, err := mutationguard.Acquire(root)
			if err != nil {
				t.Fatal(err)
			}
			err = storagepins.UpdateWithGuard(root, guard, func(p *storagepins.File) (bool, error) {
				for _, pin := range p.TaskPins()[:missing] {
					if _, err := p.RemoveTask(pin.Category, pin.ID, pin.Owner, pin.Identity); err != nil {
						return false, err
					}
				}
				return true, nil
			})
			guard.Release()
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(run, "task-disposition.json"))
			if err != nil {
				t.Fatal(err)
			}
			err = discardWorkspaceRun(context.Background(), root, state.RunID)
			if (err == nil) != tt.want {
				t.Fatalf("missing %d recovery error=%v", missing, err)
			}
			if !tt.want {
				after, err := os.ReadFile(filepath.Join(run, "task-disposition.json"))
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("partial pins changed journal: %v", err)
				}
			}
			pins, loadErr := storagepins.Load(root)
			if loadErr != nil || len(pins.TaskPins()) != 2-missing {
				t.Fatalf("recovery changed unexpected pins %v", loadErr)
			}
			durable, readErr := readTaskDisposition(run)
			if readErr != nil || durable.TaskSucceeded || durable.Policy != "retain" || !reflect.DeepEqual(durable.DiscardRequest, state.DiscardRequest) {
				t.Fatalf("missing-pin recovery changed original result %+v %v", durable, readErr)
			}
		})
	}
}

func TestWorkspaceDeletedRecoveryPinPersistenceRetry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires filesystem permission enforcement")
	}
	root, state, _ := deletedDiscardRecoveryFixture(t)
	run := filepath.Join(root, "runs", state.RunID)
	journal, err := openTaskDiscardJournal(run)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := journal.requestDiscardRecovery(); err != nil {
		t.Fatal(err)
	}
	if err := journal.transition("discarded", state.Guest, true, false); err != nil {
		t.Fatal(err)
	}
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0500); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0755) })
	err = finalizeDiscardedWorkspaceGuestWithGuard(root, guard, journal.state, state.Generation)
	guard.Release()
	if err == nil || !strings.Contains(err.Error(), "create pins tempfile") {
		t.Fatalf("expected pin persistence failure, got %v", err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	pins, err := storagepins.Load(root)
	if err != nil || len(pins.TaskPins()) != 2 {
		t.Fatalf("failed persistence changed pins: %v", err)
	}
	durable, err := readTaskDisposition(run)
	if err != nil || durable.State != "discarded" || durable.TaskSucceeded {
		t.Fatalf("discard not durable: %+v %v", durable, err)
	}
	journal.Close()
	// Model the failed CLI's subsequent exit with an observed absent predecessor.
	durable.RecoveryRequest.PID = exitedDiscardPID(t)
	writeWorkspacePinDisposition(t, run, durable)
	if err := discardWorkspaceRun(context.Background(), root, state.RunID); err != nil {
		t.Fatal(err)
	}
	got, err := readTaskDisposition(run)
	if err != nil || got.State != "discarded" || got.TaskSucceeded || got.Policy != "retain" || !reflect.DeepEqual(got.DiscardRequest, state.DiscardRequest) || got.RecoveryRequest.PID != os.Getpid() {
		t.Fatalf("retry changed original authority/result: %+v %v", got, err)
	}
	pins, err = storagepins.Load(root)
	if err != nil || len(pins.TaskPins()) != 0 {
		t.Fatalf("retry did not release pins: %v", err)
	}
}
