package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type workspaceCleanupTestGuard struct {
	held *bool
	err  error
}

func (g workspaceCleanupTestGuard) Release() error { *g.held = false; return g.err }

func workspaceCleanupFixture(t *testing.T) (taskDisposition, workspaceCleanupDeps, *[]string) {
	t.Helper()
	source := taskGuestIdentity{Path: "/source", Device: 1, Inode: 2}
	guest := taskGuestIdentity{Path: "/guest", Device: 1, Inode: 3}
	state := taskDisposition{Version: 1, RunID: "run", AttemptID: "attempt", Generation: "0123456789abcdef0123456789abcdef", OwnerPID: os.Getpid(), OwnerStartedAt: time.Now().UTC().Format(time.RFC3339Nano), Source: source.Path, SourceGuest: &source, Policy: "discard-success", State: "stopping", Guest: &guest, Owned: true, TaskSucceeded: true, Sequence: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	calls := new([]string)
	pinHeld, runHeld := false, false
	record := func(name string) { *calls = append(*calls, name) }
	deps := workspaceCleanupDeps{
		CaptureRuntime: func(taskDisposition, string) (workspaceRuntimeOwner, error) { return workspaceRuntimeOwner{}, nil },
		Identify: func(path string) (taskGuestIdentity, error) {
			record("identify")
			if path == source.Path {
				return source, nil
			}
			return guest, nil
		},
		VerifyOwner: func(taskDisposition, string) error { record("owner"); return nil },
		PinGuard: func(taskDisposition) (workspaceCleanupGuard, bool, error) {
			record("pins")
			pinHeld = true
			return workspaceCleanupTestGuard{held: &pinHeld}, false, nil
		},
		Stop:    func(context.Context, string) error { record("stop"); return nil },
		Stopped: func(context.Context, string) (bool, error) { record("stopped"); return true, nil },
		Lock: func(string) (workspaceCleanupGuard, error) {
			record("lock")
			runHeld = true
			return workspaceCleanupTestGuard{held: &runHeld}, nil
		},
		LiveRuntime: func(string) (bool, error) { record("live"); return false, nil },
		DiskHolders: func(string) ([]int, error) { record("holders"); return nil, nil },
		Delete: func(identity taskGuestIdentity) error {
			record("delete")
			if !pinHeld || !runHeld || identity != guest {
				t.Fatal("delete missing guards or bound identity")
			}
			return nil
		},
	}
	t.Cleanup(func() {
		if pinHeld || runHeld {
			t.Fatal("cleanup leaked guards")
		}
	})
	return state, deps, calls
}

func TestWorkspaceCleanupRejectsAuthority(t *testing.T) {
	tests := []struct {
		name       string
		change     func(*taskDisposition)
		generation string
	}{
		{"generation", func(*taskDisposition) {}, "different"},
		{"unowned", func(s *taskDisposition) { s.Owned = false }, ""},
		{"unsuccessful", func(s *taskDisposition) { s.TaskSucceeded = false }, ""},
		{"not-stopping", func(s *taskDisposition) { s.State = "collecting" }, ""},
		{"retain-policy", func(s *taskDisposition) { s.Policy = "retain" }, ""},
		{"missing-guest", func(s *taskDisposition) { s.Guest = nil }, ""},
		{"missing-source", func(s *taskDisposition) { s.SourceGuest = nil }, ""},
		{"same-source-path", func(s *taskDisposition) { g := *s.Guest; g.Path = s.Source; s.Guest = &g }, ""},
		{"same-source-inode", func(s *taskDisposition) { g := *s.SourceGuest; g.Path = s.Guest.Path; s.Guest = &g }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, deps, calls := workspaceCleanupFixture(t)
			tt.change(&state)
			generation := tt.generation
			if generation == "" {
				generation = state.Generation
			}
			if err := cleanupWorkspaceGuest(context.Background(), state, generation, deps); err == nil {
				t.Fatal("accepted invalid cleanup authority")
			}
			if len(*calls) != 0 {
				t.Fatalf("side effects before authority validation: %v", *calls)
			}
		})
	}
}

func TestWorkspaceCleanupGateFailures(t *testing.T) {
	failure := errors.New("unavailable")
	tests := []struct {
		name   string
		change func(*workspaceCleanupDeps)
	}{
		{"unknown-owner", func(d *workspaceCleanupDeps) { d.VerifyOwner = func(taskDisposition, string) error { return failure } }},
		{"unknown-identity", func(d *workspaceCleanupDeps) {
			d.Identify = func(string) (taskGuestIdentity, error) { return taskGuestIdentity{}, failure }
		}},
		{"changed-identity", func(d *workspaceCleanupDeps) {
			identify := d.Identify
			d.Identify = func(path string) (taskGuestIdentity, error) { i, e := identify(path); i.Inode++; return i, e }
		}},
		{"unknown-pins", func(d *workspaceCleanupDeps) {
			d.PinGuard = func(taskDisposition) (workspaceCleanupGuard, bool, error) { return nil, false, failure }
		}},
		{"operator-pin", func(d *workspaceCleanupDeps) {
			pins := d.PinGuard
			d.PinGuard = func(s taskDisposition) (workspaceCleanupGuard, bool, error) { g, _, e := pins(s); return g, true, e }
		}},
		{"stop-failure", func(d *workspaceCleanupDeps) { d.Stop = func(context.Context, string) error { return failure } }},
		{"missing-socket", func(d *workspaceCleanupDeps) {
			d.Stopped = func(context.Context, string) (bool, error) { return false, os.ErrNotExist }
		}},
		{"unknown-stop", func(d *workspaceCleanupDeps) {
			d.Stopped = func(context.Context, string) (bool, error) { return false, nil }
		}},
		{"lock-busy", func(d *workspaceCleanupDeps) {
			d.Lock = func(string) (workspaceCleanupGuard, error) { return nil, failure }
		}},
		{"live-pid", func(d *workspaceCleanupDeps) { d.LiveRuntime = func(string) (bool, error) { return true, nil } }},
		{"unknown-pid", func(d *workspaceCleanupDeps) { d.LiveRuntime = func(string) (bool, error) { return false, failure } }},
		{"open-disk", func(d *workspaceCleanupDeps) { d.DiskHolders = func(string) ([]int, error) { return []int{123}, nil } }},
		{"unknown-disk", func(d *workspaceCleanupDeps) { d.DiskHolders = func(string) ([]int, error) { return nil, failure } }},
		{"identity-replaced-during-stop", func(d *workspaceCleanupDeps) {
			identify := d.Identify
			stopped := false
			stop := d.Stop
			d.Stop = func(ctx context.Context, p string) error { stopped = true; return stop(ctx, p) }
			d.Identify = func(p string) (taskGuestIdentity, error) {
				i, e := identify(p)
				if stopped {
					i.Inode++
				}
				return i, e
			}
		}},
		{"generation-owner-changed", func(d *workspaceCleanupDeps) {
			checks := 0
			d.VerifyOwner = func(taskDisposition, string) error {
				checks++
				if checks > 1 {
					return failure
				}
				return nil
			}
		}},
		{"unqualified-deletion", func(d *workspaceCleanupDeps) { d.Delete = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, deps, calls := workspaceCleanupFixture(t)
			tt.change(&deps)
			if err := cleanupWorkspaceGuest(context.Background(), state, state.Generation, deps); err == nil {
				t.Fatal("cleanup passed failed gate")
			}
			for _, call := range *calls {
				if call == "delete" {
					t.Fatal("deleted after failed gate")
				}
			}
		})
	}
}

func TestWorkspaceCleanupHoldsGuardsThroughDelete(t *testing.T) {
	state, deps, calls := workspaceCleanupFixture(t)
	if err := cleanupWorkspaceGuest(context.Background(), state, state.Generation, deps); err != nil {
		t.Fatal(err)
	}
	want := []string{"owner", "identify", "identify", "pins", "identify", "identify", "stop", "stopped", "lock", "identify", "identify", "owner", "live", "holders", "identify", "identify", "delete"}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls %v", *calls)
	}
}

func TestWorkspaceCleanupCancellationRetains(t *testing.T) {
	state, deps, calls := workspaceCleanupFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cleanupWorkspaceGuest(ctx, state, state.Generation, deps); !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v", err)
	}
	for _, call := range *calls {
		if call == "stop" || call == "delete" {
			t.Fatalf("side effect after cancellation: %v", *calls)
		}
	}
}

func TestWorkspaceCleanupMissingSocketIsNotStopped(t *testing.T) {
	stopped, err := waitWorkspacePositivelyStopped(context.Background(), filepath.Join(t.TempDir(), "missing"))
	if stopped || err == nil {
		t.Fatalf("stopped %v, error %v", stopped, err)
	}
}

func TestWorkspaceCleanupProductionDeletionUnavailable(t *testing.T) {
	deps := defaultWorkspaceCleanupDeps()
	if deps.Delete != nil || deps.PinGuard != nil {
		t.Fatal("production cleanup enabled without coordinated quarantine and pin guard")
	}
}
