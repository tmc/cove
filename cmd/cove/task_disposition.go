package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type taskGuestIdentity struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type taskDisposition struct {
	Version        int                `json:"version"`
	RunID          string             `json:"run_id"`
	AttemptID      string             `json:"attempt_id"`
	Generation     string             `json:"owner_generation"`
	OwnerPID       int                `json:"owner_pid"`
	OwnerStartedAt string             `json:"owner_started_at"`
	SourceGuest    *taskGuestIdentity `json:"source_guest,omitempty"`
	Source         string             `json:"source"`
	Policy         string             `json:"policy"`
	State          string             `json:"state"`
	Guest          *taskGuestIdentity `json:"guest,omitempty"`
	Owned          bool               `json:"owned"`
	TaskSucceeded  bool               `json:"task_succeeded"`
	Sequence       uint64             `json:"sequence"`
	UpdatedAt      string             `json:"updated_at"`
}

type taskDispositionJournal struct {
	mu    sync.Mutex
	root  *os.Root
	lock  *os.File
	state taskDisposition
}

func identifyTaskGuest(path string) (taskGuestIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return taskGuestIdentity{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return taskGuestIdentity{}, fmt.Errorf("owned guest is not a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return taskGuestIdentity{}, fmt.Errorf("owned guest identity unavailable")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return taskGuestIdentity{}, err
	}
	return taskGuestIdentity{Path: absolute, Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}, nil
}

func newTaskDispositionJournal(dir, runID, source, policy string) (*taskDispositionJournal, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	directory, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	// Root.OpenFile resolves in-root symlinks; openat preserves O_NOFOLLOW.
	fd, err := unix.Openat(int(directory.Fd()), "task-disposition.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	directory.Close()
	if err != nil {
		root.Close()
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), "task-disposition.lock")
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		root.Close()
		return nil, err
	}
	defer func() {
		if root != nil {
			lock.Close()
			root.Close()
		}
	}()
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("task disposition lock is not a regular file")
	}
	if _, err := root.Lstat("task-disposition.json"); !os.IsNotExist(err) {
		return nil, fmt.Errorf("task disposition already exists or is unavailable")
	}
	var sourceGuest *taskGuestIdentity
	if source != "" {
		identity, err := identifyTaskGuest(source)
		if err != nil {
			return nil, fmt.Errorf("identify task source: %w", err)
		}
		sourceGuest = &identity
		source = identity.Path
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	started := processStartedAt(os.Getpid())
	journal := &taskDispositionJournal{root: root, lock: lock, state: taskDisposition{Version: 1, RunID: runID, AttemptID: runID + "-" + hex.EncodeToString(token[:]), Generation: hex.EncodeToString(token[:]), OwnerPID: os.Getpid(), Source: source, SourceGuest: sourceGuest, Policy: policy, State: "resolving"}}
	if !started.IsZero() {
		journal.state.OwnerStartedAt = started.Format(time.RFC3339Nano)
	}
	if err := validateTaskDisposition(journal.state, false); err != nil {
		return nil, err
	}
	if err := journal.persist(); err != nil {
		return nil, err
	}
	root = nil
	return journal, nil
}

func (j *taskDispositionJournal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return nil
	}
	err := j.lock.Close()
	j.lock = nil
	return errors.Join(err, j.root.Close())
}

func (j *taskDispositionJournal) persist() error {
	j.state.Sequence++
	j.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.MarshalIndent(j.state, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return fmt.Errorf("task disposition exceeds bounds")
	}
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	name := ".task-disposition-" + hex.EncodeToString(token[:])
	file, err := j.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer j.root.Remove(name)
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := j.root.Rename(name, "task-disposition.json"); err != nil {
		return err
	}
	directory, err := j.root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func taskDispositionTransitionAllowed(from, to string) bool {
	if from == "retained" || from == "discarded" || from == "cleanup_unknown" || from == "interrupted" {
		return false
	}
	if !validTaskDispositionState(from) || !validTaskDispositionState(to) {
		return false
	}
	if from == to {
		return true
	}
	if to == "interrupted" {
		return true
	}
	if to == "collecting" {
		return from != "stopping"
	}
	if to == "retained" || to == "cleanup_unknown" {
		return true
	}
	switch from {
	case "resolving":
		return to == "preparing"
	case "preparing":
		return to == "ready"
	case "ready":
		return to == "executing"
	case "collecting":
		return to == "stopping"
	case "stopping":
		return to == "discarded"
	}
	return false
}

func (j *taskDispositionJournal) transition(state string, guest *taskGuestIdentity, owned, succeeded bool) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.lock == nil {
		return fmt.Errorf("task disposition journal is closed")
	}
	if !taskDispositionTransitionAllowed(j.state.State, state) {
		return fmt.Errorf("invalid task disposition transition: %s to %s", j.state.State, state)
	}
	if j.state.Guest != nil && (guest == nil || *guest != *j.state.Guest || owned != j.state.Owned) {
		return fmt.Errorf("assigned task guest identity is immutable")
	}
	if j.state.TaskSucceeded && !succeeded {
		return fmt.Errorf("task success is immutable")
	}
	previous := j.state
	j.state.State = state
	if guest != nil {
		identity := *guest
		j.state.Guest = &identity
	} else {
		j.state.Guest = nil
	}
	j.state.Owned = owned
	j.state.TaskSucceeded = succeeded
	if err := validateTaskDisposition(j.state, false); err != nil {
		j.state = previous
		return err
	}
	if err := j.persist(); err != nil {
		j.state = previous
		return err
	}
	return nil
}

func readTaskDisposition(dir string) (taskDisposition, error) {
	var state taskDisposition
	root, err := os.OpenRoot(dir)
	if err != nil {
		return state, err
	}
	defer root.Close()
	info, err := root.Lstat("task-disposition.json")
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return state, fmt.Errorf("task disposition is not a bounded regular file")
	}
	file, err := root.OpenFile("task-disposition.json", os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return state, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return state, err
	}
	if !opened.Mode().IsRegular() || opened.Size() > 64<<10 || !os.SameFile(info, opened) {
		return state, fmt.Errorf("task disposition changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return state, err
	}
	if len(data) > 64<<10 {
		return state, fmt.Errorf("task disposition exceeds bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return state, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return state, fmt.Errorf("task disposition has trailing data")
	}
	if err := validateTaskDisposition(state, true); err != nil {
		return state, err
	}
	return state, nil
}

func validTaskDispositionState(state string) bool {
	switch state {
	case "resolving", "preparing", "ready", "executing", "collecting", "stopping", "retained", "discarded", "cleanup_unknown", "interrupted":
		return true
	}
	return false
}

func validTaskGuestIdentity(identity *taskGuestIdentity) bool {
	return identity != nil && filepath.IsAbs(identity.Path) && filepath.Clean(identity.Path) == identity.Path && identity.Inode != 0
}

func validateTaskDisposition(state taskDisposition, persisted bool) error {
	if state.Version != 1 || state.RunID == "" || state.AttemptID == "" || len(state.Generation) != 32 || state.OwnerPID <= 0 {
		return fmt.Errorf("invalid task disposition identity")
	}
	if _, err := hex.DecodeString(state.Generation); err != nil {
		return fmt.Errorf("invalid task owner generation")
	}
	if !validTaskDispositionState(state.State) || (state.Policy != "retain" && state.Policy != "discard-success") {
		return fmt.Errorf("invalid task disposition state or policy")
	}
	if state.OwnerStartedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, state.OwnerStartedAt); err != nil {
			return fmt.Errorf("invalid task owner start time")
		}
	}
	if persisted {
		if state.Sequence == 0 {
			return fmt.Errorf("invalid task disposition sequence")
		}
		if _, err := time.Parse(time.RFC3339Nano, state.UpdatedAt); err != nil {
			return fmt.Errorf("invalid task disposition update time")
		}
	}
	if state.Source != "" {
		if !validTaskGuestIdentity(state.SourceGuest) || state.Source != state.SourceGuest.Path {
			return fmt.Errorf("invalid task source identity")
		}
	} else if state.SourceGuest != nil {
		return fmt.Errorf("unexpected task source identity")
	}
	if state.Guest != nil && !validTaskGuestIdentity(state.Guest) {
		return fmt.Errorf("invalid task guest identity")
	}
	if (state.State == "ready" || state.State == "executing" || state.TaskSucceeded) && state.Guest == nil {
		return fmt.Errorf("task guest identity required")
	}
	if state.Owned {
		if state.Guest == nil || state.SourceGuest == nil || state.Guest.Path == state.SourceGuest.Path || (state.Guest.Device == state.SourceGuest.Device && state.Guest.Inode == state.SourceGuest.Inode) {
			return fmt.Errorf("owned guest must differ from source")
		}
	}
	if (state.State == "stopping" || state.State == "discarded") && (!state.Owned || !state.TaskSucceeded || state.Policy != "discard-success") {
		return fmt.Errorf("task discard requires owned successful guest and discard-success policy")
	}
	if state.TaskSucceeded && (state.State == "resolving" || state.State == "preparing" || state.State == "ready" || state.State == "executing") {
		return fmt.Errorf("task success requires collected result")
	}
	return nil
}

// taskDispositionOwnerStatus reports unknown when process identity cannot be verified.
func taskDispositionOwnerStatus(state taskDisposition, generation string, observe func(int) (time.Time, bool, error)) (string, error) {
	if generation != state.Generation {
		return "unknown", fmt.Errorf("task owner generation differs")
	}
	if err := validateTaskDisposition(state, true); err != nil {
		return "unknown", err
	}
	started, exists, err := observe(state.OwnerPID)
	if err != nil {
		return "unknown", err
	}
	if !exists {
		return "interrupted", nil
	}
	if started.IsZero() || state.OwnerStartedAt == "" {
		return "unknown", nil
	}
	expected, _ := time.Parse(time.RFC3339Nano, state.OwnerStartedAt)
	if !started.Equal(expected) {
		return "interrupted", nil
	}
	return "active", nil
}
