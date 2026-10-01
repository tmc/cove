package storagepins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
)

func testTaskPin() TaskPin {
	return TaskPin{Category: "vm", ID: "workspace", AddedAt: time.Unix(100, 0).UTC(), Owner: TaskOwner{RunID: "run", AttemptID: "attempt", Generation: "0123456789abcdef0123456789abcdef", PID: 123, StartedAt: "2026-10-01T12:00:00Z"}, Identity: DirectoryIdentity{Path: "/guest/workspace", Device: 1, Inode: 2}}
}

func TestTaskPinsIndependentOperatorAndGeneration(t *testing.T) {
	pins := New()
	first := testTaskPin()
	second := first
	second.Owner.Generation = "1123456789abcdef0123456789abcdef"
	if err := pins.Add("vm", "workspace", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, pin := range []TaskPin{first, second} {
		if err := pins.AddTask(pin); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pins.Remove("vm", "workspace"); err != nil {
		t.Fatal(err)
	}
	if pins.IsOperatorPinned("vm", "workspace") || !pins.IsPinned("vm", "workspace") {
		t.Fatal("operator removal changed task protection")
	}
	if _, err := pins.RemoveTask(first.Category, first.ID, first.Owner, first.Identity); err != nil {
		t.Fatal(err)
	}
	if !pins.IsPinned("vm", "workspace") || len(pins.TaskPins()) != 1 {
		t.Fatal("removed another generation")
	}
	if err := pins.Add("vm", "workspace", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := pins.RemoveTask(second.Category, second.ID, second.Owner, second.Identity); err != nil {
		t.Fatal(err)
	}
	if !pins.IsPinned("vm", "workspace") || !pins.IsOperatorPinned("vm", "workspace") {
		t.Fatal("task release removed operator pin")
	}
}

func TestTaskPinWrongOwnerAndIdentityRefused(t *testing.T) {
	tests := []struct {
		name   string
		change func(*TaskPin)
	}{
		{"generation", func(p *TaskPin) { p.Owner.Generation = "1123456789abcdef0123456789abcdef" }},
		{"pid", func(p *TaskPin) { p.Owner.PID++ }},
		{"start", func(p *TaskPin) { p.Owner.StartedAt = "2026-10-01T12:00:01Z" }},
		{"run", func(p *TaskPin) { p.Owner.RunID = "other" }},
		{"attempt", func(p *TaskPin) { p.Owner.AttemptID = "other" }},
		{"inode", func(p *TaskPin) { p.Identity.Inode++ }},
		{"path", func(p *TaskPin) { p.Identity.Path = "/other" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pins := New()
			pin := testTaskPin()
			if err := pins.AddTask(pin); err != nil {
				t.Fatal(err)
			}
			tt.change(&pin)
			if _, err := pins.RemoveTask(pin.Category, pin.ID, pin.Owner, pin.Identity); err == nil {
				t.Fatal("accepted mismatched release")
			}
			if len(pins.TaskPins()) != 1 {
				t.Fatal("mismatched release changed pins")
			}
		})
	}
}

func TestTaskPinBoundsAndMalformedRecords(t *testing.T) {
	tests := []struct {
		name   string
		change func(*TaskPin)
	}{
		{"bad-generation", func(p *TaskPin) { p.Owner.Generation = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" }},
		{"unknown-start", func(p *TaskPin) { p.Owner.StartedAt = "" }},
		{"unknown-pid", func(p *TaskPin) { p.Owner.PID = 0 }},
		{"relative-path", func(p *TaskPin) { p.Identity.Path = "relative" }},
		{"unknown-inode", func(p *TaskPin) { p.Identity.Inode = 0 }},
		{"root-path", func(p *TaskPin) { p.Identity.Path = "/" }},
		{"missing-time", func(p *TaskPin) { p.AddedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pin := testTaskPin()
			tt.change(&pin)
			if err := New().AddTask(pin); err == nil {
				t.Fatal("accepted malformed task pin")
			}
			root := t.TempDir()
			data, _ := json.Marshal(onDisk{TaskPins: []TaskPin{pin}})
			if err := os.WriteFile(filepath.Join(root, Filename), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil {
				t.Fatal("silently dropped malformed task pin")
			}
		})
	}
}

func TestTaskPinGuardedRoundTripAndUnion(t *testing.T) {
	root := t.TempDir()
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	pin := testTaskPin()
	if err := UpdateWithGuard(root, guard, func(pins *File) (bool, error) {
		pins.Add("run", "operator", time.Now())
		return true, pins.AddTask(pin)
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.TaskPins()) != 1 || loaded.TaskPins()[0].Owner != pin.Owner || loaded.TaskPins()[0].Identity != pin.Identity {
		t.Fatalf("task pins %v", loaded.TaskPins())
	}
	refs := loaded.RefSet()
	if !refs["run:operator"] || !refs["vm:workspace"] {
		t.Fatalf("refs %v", refs)
	}
	other, err := mutationguard.Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	if err := UpdateWithGuard(root, other, func(*File) (bool, error) { t.Fatal("wrong-root callback ran"); return true, nil }); err == nil {
		t.Fatal("accepted wrong-root guard")
	}
	if err := UpdateWithGuard(root, guard, func(pins *File) (bool, error) {
		_, err := pins.RemoveTask(pin.Category, pin.ID, pin.Owner, pin.Identity)
		return true, err
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(root)
	if err != nil || len(loaded.TaskPins()) != 0 || !loaded.IsOperatorPinned("run", "operator") {
		t.Fatalf("loaded %v, error %v", loaded, err)
	}
}

func TestOperatorSnapshotSavePreservesTaskPins(t *testing.T) {
	root := t.TempDir()
	pin := testTaskPin()
	if err := Update(root, func(pins *File) (bool, error) { return true, pins.AddTask(pin) }); err != nil {
		t.Fatal(err)
	}
	operatorSnapshot := New()
	operatorSnapshot.Add("run", "operator", time.Now())
	if err := Save(root, operatorSnapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || !loaded.IsPinned("vm", "workspace") || !loaded.IsOperatorPinned("run", "operator") {
		t.Fatalf("loaded %v, error %v", loaded, err)
	}
}
