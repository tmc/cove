package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func exitedDiscardPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestWorkspaceOperatorDiscardJournal(t *testing.T) {
	j, dir, guest := taskJournalForTest(t)
	if err := j.transition("preparing", &guest, true, false); err != nil {
		t.Fatal(err)
	}
	if err := j.transition("retained", &guest, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := openTaskDiscardJournal(dir); err == nil {
		t.Fatal("opened locked journal")
	}
	if err := j.requestDiscard(); err == nil {
		t.Fatal("accepted live coordinator")
	}
	j.state.OwnerPID = exitedDiscardPID(t)
	j.state.Policy = "retain"
	if err := j.persist(); err != nil {
		t.Fatal(err)
	}
	before := j.state
	j.Close()
	operator, err := openTaskDiscardJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	if err := operator.requestDiscard(); err != nil {
		t.Fatal(err)
	}
	got, err := readTaskDisposition(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaskSucceeded || got.State != "stopping" || got.DiscardRequest == nil {
		t.Fatalf("invalid discard state: %+v", got)
	}
	original := got
	original.State = before.State
	original.Sequence = before.Sequence
	original.UpdatedAt = before.UpdatedAt
	original.DiscardRequest = nil
	if !reflect.DeepEqual(original, before) {
		t.Fatalf("operator mutated original task identity: %+v", got)
	}
	if err := operator.requestDiscard(); err == nil {
		t.Fatal("accepted active discard actor")
	}
	if err := operator.transition("discarded", got.Guest, true, false); err != nil {
		t.Fatal(err)
	}
	final, err := readTaskDisposition(dir)
	if err != nil || final.TaskSucceeded || final.State != "discarded" {
		t.Fatalf("final %+v err %v", final, err)
	}
}

func TestWorkspaceOperatorDiscardAuthority(t *testing.T) {
	state, _, _ := workspaceCleanupFixture(t)
	state.OwnerPID = exitedDiscardPID(t)
	state.Policy = "retain"
	state.TaskSucceeded = false
	state.DiscardRequest = &taskDiscardRequest{PID: os.Getpid(), StartedAt: processStartedAt(os.Getpid()).UTC().Format(time.RFC3339Nano), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, tt := range []struct {
		name   string
		change func(*taskDisposition)
		want   bool
	}{
		{"valid", func(*taskDisposition) {}, true},
		{"live-coordinator", func(s *taskDisposition) { s.OwnerPID = os.Getpid() }, false},
		{"wrong-actor", func(s *taskDisposition) { r := *s.DiscardRequest; r.PID = exitedDiscardPID(t); s.DiscardRequest = &r }, false},
		{"reused-actor", func(s *taskDisposition) {
			r := *s.DiscardRequest
			r.StartedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			s.DiscardRequest = &r
		}, false},
		{"unowned", func(s *taskDisposition) { s.Owned = false }, false},
		{"no-request", func(s *taskDisposition) { s.DiscardRequest = nil }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := state
			tt.change(&s)
			err := verifyWorkspaceDiscardActor(s, s.Generation)
			if (err == nil) != tt.want {
				t.Fatalf("authority error %v", err)
			}
		})
	}
}

func TestWorkspaceOperatorDiscardCleanupFailure(t *testing.T) {
	state, deps, calls := workspaceCleanupFixture(t)
	state.OwnerPID = exitedDiscardPID(t)
	state.TaskSucceeded = false
	state.Policy = "retain"
	state.DiscardRequest = &taskDiscardRequest{PID: os.Getpid(), StartedAt: processStartedAt(os.Getpid()).UTC().Format(time.RFC3339Nano), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	deps.VerifyOwner = verifyWorkspaceDiscardActor
	deps.DiskHolders = func(string) ([]int, error) { return []int{123}, nil }
	if err := cleanupWorkspaceGuest(context.Background(), state, state.Generation, deps); err == nil {
		t.Fatal("deleted held guest")
	}
	for _, call := range *calls {
		if call == "delete" {
			t.Fatal("deletion after failed gate")
		}
	}
	if state.TaskSucceeded {
		t.Fatal("failed task became success")
	}
}

func TestWorkspaceExitedRuntimeCapture(t *testing.T) {
	guest, err := identifyTaskGuest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	generation := "0123456789abcdef0123456789abcdef"
	state := taskDisposition{Guest: &guest, Generation: generation}
	dir := filepath.Join(guest.Path, "runtime")
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(guest.Path, "workspace-runtime-diagnostics.json"), []byte(`{"directory":"runtime"}`), 0600)
	start := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	owner := workspaceRuntimeOwner{Generation: generation, PID: exitedDiscardPID(t), StartedAt: start}
	exit := workspaceRuntimeExit{Generation: generation, PID: owner.PID, StartedAt: start, EndedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	write := func(name string, value any) {
		t.Helper()
		data, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("running.json", owner)
	write("exit.json", exit)
	if _, err := captureExitedWorkspaceRuntimeOwner(state, generation); err != nil {
		t.Fatal(err)
	}
	exit.Generation = "ffffffffffffffffffffffffffffffff"
	write("exit.json", exit)
	if _, err := captureExitedWorkspaceRuntimeOwner(state, generation); err == nil {
		t.Fatal("accepted unrelated exit")
	}
	exit.Generation = generation
	owner.PID = os.Getpid()
	exit.PID = owner.PID
	write("running.json", owner)
	write("exit.json", exit)
	if _, err := captureExitedWorkspaceRuntimeOwner(state, generation); err == nil {
		t.Fatal("accepted active or reused native PID")
	}
}

func TestWorkspaceOperatorDiscardRejectsNonterminal(t *testing.T) {
	for _, state := range []string{"resolving", "preparing", "ready", "executing", "collecting", "discarded"} {
		t.Run(state, func(t *testing.T) {
			j, _, guest := taskJournalForTest(t)
			j.state.Guest = &guest
			j.state.Owned = true
			j.state.OwnerPID = exitedDiscardPID(t)
			j.state.State = state
			before := j.state
			if err := j.requestDiscard(); err == nil {
				t.Fatal("accepted nonterminal or already discarded run")
			}
			if !reflect.DeepEqual(j.state, before) {
				t.Fatal("rejection changed authority")
			}
		})
	}
}
