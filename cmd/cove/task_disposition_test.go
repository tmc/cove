package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func taskJournalForTest(t *testing.T) (*taskDispositionJournal, string, taskGuestIdentity) {
	t.Helper()
	dir := t.TempDir()
	source := t.TempDir()
	guest, err := identifyTaskGuest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	j, err := newTaskDispositionJournal(dir, "run", source, "discard-success")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j, dir, guest
}

func TestTaskDispositionTransitions(t *testing.T) {
	j, dir, guest := taskJournalForTest(t)
	if err := j.transition("preparing", &guest, true, false); err != nil {
		t.Fatal(err)
	}
	guest.Path = "/changed"
	state, err := readTaskDisposition(dir)
	if err != nil {
		t.Fatal(err)
	}
	if state.Guest.Path == guest.Path {
		t.Fatal("caller mutated stored identity")
	}
	if err := j.transition("ready", &guest, true, false); err == nil {
		t.Fatal("accepted changed guest")
	}
	if err := j.transition("stopping", state.Guest, true, true); err == nil {
		t.Fatal("accepted uncollected discard")
	}
	if err := j.transition("interrupted", state.Guest, true, false); err != nil {
		t.Fatal(err)
	}
	if err := j.transition("interrupted", state.Guest, true, false); err == nil {
		t.Fatal("accepted terminal mutation")
	}
	state, err = readTaskDisposition(dir)
	if err != nil || state.State != "interrupted" {
		t.Fatalf("state %+v, error %v", state, err)
	}
}

func TestTaskDispositionLockAndFailure(t *testing.T) {
	dir := t.TempDir()
	source := t.TempDir()
	if _, err := newTaskDispositionJournal(dir, "run", source, "bad"); err == nil {
		t.Fatal("accepted bad policy")
	}
	j, err := newTaskDispositionJournal(dir, "run", source, "retain")
	if err != nil {
		t.Fatalf("failed constructor leaked lock: %v", err)
	}
	if _, err := newTaskDispositionJournal(dir, "run", source, "retain"); err == nil {
		t.Fatal("accepted concurrent owner")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := newTaskDispositionJournal(dir, "run", source, "retain"); err == nil {
		t.Fatal("overwrote existing disposition")
	}
	if err := os.Remove(filepath.Join(dir, "task-disposition.json")); err != nil {
		t.Fatal(err)
	}
	j, err = newTaskDispositionJournal(dir, "other", source, "retain")
	if err != nil {
		t.Fatalf("stale lock prevents new journal: %v", err)
	}
	j.Close()
}

func TestTaskDispositionRejectsInvalidRecords(t *testing.T) {
	j, dir, _ := taskJournalForTest(t)
	valid := j.state
	j.Close()
	tests := []struct {
		name   string
		mutate func(*taskDisposition)
	}{
		{"state", func(s *taskDisposition) { s.State = "invalid" }},
		{"policy", func(s *taskDisposition) { s.Policy = "delete" }},
		{"generation", func(s *taskDisposition) { s.Generation = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" }},
		{"source", func(s *taskDisposition) { s.SourceGuest = nil }},
		{"owner", func(s *taskDisposition) { s.OwnerPID = 0 }},
		{"started", func(s *taskDisposition) { s.OwnerStartedAt = "bad" }},
		{"updated", func(s *taskDisposition) { s.UpdatedAt = "bad" }},
		{"sequence", func(s *taskDisposition) { s.Sequence = 0 }},
		{"owned", func(s *taskDisposition) { s.Owned = true }},
		{"discard", func(s *taskDisposition) { s.State = "discarded" }},
		{"premature-success", func(s *taskDisposition) { s.TaskSucceeded = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := valid
			tt.mutate(&state)
			data, _ := json.Marshal(state)
			if err := os.WriteFile(filepath.Join(dir, "task-disposition.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readTaskDisposition(dir); err == nil {
				t.Fatal("accepted invalid record")
			}
		})
	}
	data, _ := json.Marshal(valid)
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"trailing", append(append([]byte{}, data...), []byte(" {}")...)},
		{"unknown-field", append([]byte(`{"unknown":true,`), data[1:]...)},
		{"oversized", make([]byte, (64<<10)+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			os.WriteFile(filepath.Join(dir, "task-disposition.json"), tt.data, 0600)
			if _, err := readTaskDisposition(dir); err == nil {
				t.Fatal("accepted invalid document")
			}
		})
	}
}

func TestTaskDispositionOwnerObservation(t *testing.T) {
	j, _, _ := taskJournalForTest(t)
	state := j.state
	started := time.Now().UTC().Truncate(time.Second)
	state.OwnerStartedAt = started.Format(time.RFC3339Nano)
	tests := []struct {
		name       string
		generation string
		observed   time.Time
		exists     bool
		err        error
		want       string
		wantErr    bool
	}{
		{"active", state.Generation, started, true, nil, "active", false},
		{"dead", state.Generation, time.Time{}, false, nil, "interrupted", false},
		{"reused-pid", state.Generation, started.Add(time.Second), true, nil, "interrupted", false},
		{"unknown-start", state.Generation, time.Time{}, true, nil, "unknown", false},
		{"inspection-failure", state.Generation, time.Time{}, false, errors.New("unavailable"), "unknown", true},
		{"generation", "other", started, true, nil, "unknown", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := taskDispositionOwnerStatus(state, tt.generation, func(int) (time.Time, bool, error) { return tt.observed, tt.exists, tt.err })
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("got %s, %v", got, err)
			}
		})
	}
}

func TestTaskDispositionSuccessfulDiscard(t *testing.T) {
	j, dir, guest := taskJournalForTest(t)
	if err := j.transition("preparing", j.state.SourceGuest, true, false); err == nil {
		t.Fatal("accepted source as owned guest")
	}
	for _, state := range []string{"preparing", "ready", "executing", "collecting", "stopping", "discarded"} {
		succeeded := state == "collecting" || state == "stopping" || state == "discarded"
		if err := j.transition(state, &guest, true, succeeded); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
	}
	state, err := readTaskDisposition(dir)
	if err != nil || state.State != "discarded" || !state.TaskSucceeded {
		t.Fatalf("state %+v, error %v", state, err)
	}
}

func TestTaskDispositionRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	source := t.TempDir()
	link := filepath.Join(t.TempDir(), "source")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := newTaskDispositionJournal(dir, "run", link, "retain"); err == nil {
		t.Fatal("accepted symlink source")
	}
	if err := os.Remove(filepath.Join(dir, "task-disposition.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "lock"), filepath.Join(dir, "task-disposition.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := newTaskDispositionJournal(dir, "run", source, "retain"); err == nil {
		t.Fatal("accepted symlink lock")
	}
}

func TestTaskDispositionRejectsLocalSymlinkLock(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "other.lock")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.lock", filepath.Join(dir, "task-disposition.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := newTaskDispositionJournal(dir, "run", t.TempDir(), "retain"); err == nil {
		t.Fatal("accepted in-root symlink lock")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("target %q, error %v", data, err)
	}
}
