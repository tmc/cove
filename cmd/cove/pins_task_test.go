package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestPinsTaskProtectionVisibleAndIndependent(t *testing.T) {
	root := t.TempDir()
	t.Setenv(vmconfig.StateDirEnv, root)
	owner := storagepins.TaskOwner{RunID: "run-test", AttemptID: "attempt", Generation: "0123456789abcdef0123456789abcdef", PID: 123, StartedAt: "2026-10-01T12:00:00Z"}
	if err := storagepins.Update(root, func(f *storagepins.File) (bool, error) {
		if err := f.Add("vm", "guest", time.Now()); err != nil {
			return false, err
		}
		return true, f.AddTask(storagepins.TaskPin{Category: "vm", ID: "guest", AddedAt: time.Now(), Owner: owner, Identity: storagepins.DirectoryIdentity{Path: filepath.Join(root, "vms", "guest.covevm"), Device: 1, Inode: 2}})
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	env := commandTestEnv()
	env.Stdout = &out
	if err := runPinsList(env, []string{"-json"}); err != nil {
		t.Fatal(err)
	}
	var rows []pinsListRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Source != "operator" || rows[1].Source != "task" || rows[1].Owner == nil || *rows[1].Owner != owner || rows[1].Identity == nil {
		t.Fatalf("rows=%+v", rows)
	}
	out.Reset()
	if err := runPinsList(env, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"operator", "task", owner.RunID, owner.Generation, "123"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	for i := 0; i < 2; i++ {
		out.Reset()
		if err := handleUnpinCommand(env, []string{"vm:guest"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "task protection remains") || strings.Contains(out.String(), "not pinned") {
			t.Fatalf("misleading unpin output %q", out.String())
		}
		pins, err := storagepins.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if pins.IsOperatorPinned("vm", "guest") || !pins.IsPinned("vm", "guest") || len(pins.TaskPins()) != 1 {
			t.Fatal("operator unpin changed task protection")
		}
	}
}
