package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
)

func workspaceQuarantineFixture(t *testing.T) (string, taskDisposition, *mutationguard.Guard) {
	t.Helper()
	raw := t.TempDir()
	root, err := filepath.EvalSymlinks(raw)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "vms")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(parent, "source.covevm")
	guest := filepath.Join(parent, "owned.covevm")
	for _, dir := range []string{source, guest} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(guest, "disk.img"), []byte("guest data"), 0600); err != nil {
		t.Fatal(err)
	}
	sourceID, err := identifyTaskGuest(source)
	if err != nil {
		t.Fatal(err)
	}
	guestID, err := identifyTaskGuest(guest)
	if err != nil {
		t.Fatal(err)
	}
	state := taskDisposition{Version: 1, RunID: "run", AttemptID: "attempt", Generation: strings.Repeat("a", 32), OwnerPID: os.Getpid(), Source: source, SourceGuest: &sourceID, Policy: "discard-success", State: "stopping", Guest: &guestID, Owned: true, TaskSucceeded: true, Sequence: 3, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { guard.Release() })
	return root, state, guard
}
func TestWorkspaceQuarantineRoundTrip(t *testing.T) {
	root, state, guard := workspaceQuarantineFixture(t)
	receipt, err := quarantineWorkspaceGuest(root, state, state.Generation, guard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(state.Guest.Path); !os.IsNotExist(err) {
		t.Fatalf("original remains: %v", err)
	}
	moved := filepath.Join(root, "vms", workspaceQuarantineDirectory, receipt.Name)
	identity, err := identifyTaskGuest(moved)
	if err != nil || identity.Device != state.Guest.Device || identity.Inode != state.Guest.Inode {
		t.Fatalf("moved identity %+v %v", identity, err)
	}
	found, err := discoverWorkspaceQuarantines(root)
	if err != nil || len(found) != 1 || found[0].Phase != "moved" {
		t.Fatalf("discovery %+v %v", found, err)
	}
	if err := deleteWorkspaceQuarantine(root, receipt, state.Generation, guard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(moved); !os.IsNotExist(err) {
		t.Fatalf("quarantine remains: %v", err)
	}
	found, err = discoverWorkspaceQuarantines(root)
	if err != nil || len(found) != 1 || found[0].Phase != "deleted" {
		t.Fatalf("receipt lost %+v %v", found, err)
	}
	if _, err := os.Stat(state.Source); err != nil {
		t.Fatalf("source changed: %v", err)
	}
}
func TestWorkspaceQuarantineRefusesInvalidTargets(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string, *taskDisposition, *mutationguard.Guard)
	}{
		{"generation", func(t *testing.T, r string, s *taskDisposition, g *mutationguard.Guard) {
			s.Generation = strings.Repeat("b", 32)
		}},
		{"retained", func(t *testing.T, r string, s *taskDisposition, g *mutationguard.Guard) { s.State = "retained" }},
		{"identity", func(t *testing.T, r string, s *taskDisposition, g *mutationguard.Guard) { s.Guest.Inode++ }},
		{"guest symlink", func(t *testing.T, r string, s *taskDisposition, g *mutationguard.Guard) {
			os.Rename(s.Guest.Path, s.Guest.Path+"-original")
			os.Symlink(s.Source, s.Guest.Path)
		}},
		{"parent symlink", func(t *testing.T, r string, s *taskDisposition, g *mutationguard.Guard) {
			os.Rename(filepath.Join(r, "vms"), filepath.Join(r, "real-vms"))
			os.Symlink("real-vms", filepath.Join(r, "vms"))
		}},
		{"released guard", func(t *testing.T, r string, s *taskDisposition, g *mutationguard.Guard) { g.Release() }},
		{"collision", func(t *testing.T, r string, s *taskDisposition, g *mutationguard.Guard) {
			os.MkdirAll(filepath.Join(r, "vms", workspaceQuarantineDirectory, s.Generation+".covevm"), 0700)
		}},
		{"receipt collision", func(t *testing.T, r string, s *taskDisposition, g *mutationguard.Guard) {
			q := filepath.Join(r, "vms", workspaceQuarantineDirectory)
			os.Mkdir(q, 0700)
			os.WriteFile(filepath.Join(q, s.Generation+".json"), []byte("operator"), 0600)
		}},
		{"namespace symlink", func(t *testing.T, r string, s *taskDisposition, g *mutationguard.Guard) {
			os.Symlink("source.covevm", filepath.Join(r, "vms", workspaceQuarantineDirectory))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, state, guard := workspaceQuarantineFixture(t)
			tt.change(t, root, &state, guard)
			if _, err := quarantineWorkspaceGuest(root, state, strings.Repeat("a", 32), guard); err == nil {
				t.Fatal("unsafe quarantine succeeded")
			}
		})
	}
}
func TestWorkspaceQuarantineFailedReceiptUpdateIsDiscoverable(t *testing.T) {
	root, state, guard := workspaceQuarantineFixture(t)
	q := filepath.Join(root, "vms", workspaceQuarantineDirectory)
	if err := os.Mkdir(q, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(q, state.Generation+".json.pending"), []byte("collision"), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := quarantineWorkspaceGuest(root, state, state.Generation, guard)
	if err == nil {
		t.Fatal("receipt update unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(q, receipt.Name, "disk.img")); err != nil {
		t.Fatal(err)
	}
	found, err := discoverWorkspaceQuarantines(root)
	if err != nil || len(found) != 1 || found[0].Phase != "prepared" {
		t.Fatalf("missing recovery receipt %+v %v", found, err)
	}
	if err := deleteWorkspaceQuarantine(root, receipt, state.Generation, guard); err == nil {
		t.Fatal("deleted without committed moved receipt")
	}
}
func TestWorkspaceQuarantineDeletionRejectsChangedIdentity(t *testing.T) {
	root, state, guard := workspaceQuarantineFixture(t)
	receipt, err := quarantineWorkspaceGuest(root, state, state.Generation, guard)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "vms", workspaceQuarantineDirectory, receipt.Name)
	if err := os.Rename(path, path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "operator"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := deleteWorkspaceQuarantine(root, receipt, state.Generation, guard); err == nil {
		t.Fatal("deleted replacement directory")
	}
	if _, err := os.Stat(filepath.Join(path, "operator")); err != nil {
		t.Fatal(err)
	}
	if found, err := discoverWorkspaceQuarantines(root); err != nil || len(found) != 1 {
		t.Fatalf("receipt lost %v %v", found, err)
	}
}

func TestWorkspaceQuarantineDeletionDoesNotFollowGuestSymlinks(t *testing.T) {
	root, state, guard := workspaceQuarantineFixture(t)
	outside := filepath.Join(root, "operator-data")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(outside, "keep")
	if err := os.WriteFile(file, []byte("operator"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(state.Guest.Path, "linked-directory")); err != nil {
		t.Fatal(err)
	}
	receipt, err := quarantineWorkspaceGuest(root, state, state.Generation, guard)
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteWorkspaceQuarantine(root, receipt, state.Generation, guard); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "operator" {
		t.Fatalf("outside data changed: %q %v", data, err)
	}
}
