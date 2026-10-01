package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestWorkspaceOwnedForkReplacementIsNotAdopted(t *testing.T) {
	o := workspaceTestOptions(t)
	o.From = "base"
	o.Retain = "discard-success"
	p, err := planGoWorkspace(o)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	d := workspaceTestDeps(t, &steps)
	parent := t.TempDir()
	child := filepath.Join(parent, "child.covevm")
	var original taskGuestIdentity
	resolves := 0
	d.Resolve = func(workspacePlan) (string, bool, error) { resolves++; return child, resolves > 1, nil }
	d.IdentifyGuest = identifyTaskGuest
	d.ForkOwned = func(from, to string, source taskGuestIdentity) (taskGuestIdentity, error) {
		if source.Path == "" || source.Inode == 0 {
			t.Fatal("fork lacked recorded source identity")
		}
		if err := os.Mkdir(child, 0700); err != nil {
			t.Fatal(err)
		}
		original, err = identifyTaskGuest(child)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(child, child+"-original"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(child, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(child, "operator"), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		return original, nil
	}
	d.PinOwned = func(taskDisposition, string) error { t.Fatal("pinned replacement"); return nil }
	d.Start = func(workspacePlan, string) error { t.Fatal("started replacement"); return nil }
	d.StartOwned = func(workspacePlan, string, string) error { t.Fatal("started replacement"); return nil }
	d.Task = func(context.Context, workspaceOptions, workspacePlan, string, commandEnv) (*controlpb.AgentExecResponse, error) {
		t.Fatal("ran task in replacement")
		return nil, nil
	}
	d.Discard = func(workspacePlan, string) error { t.Fatal("discarded replacement"); return nil }
	d.DiscardOwned = func(context.Context, taskDisposition, string) error { t.Fatal("discarded replacement"); return nil }
	receipt, err := openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard, Stderr: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "identity changed before preparation") || receipt.Disposition != "retained" {
		t.Fatalf("receipt %+v error %v", receipt, err)
	}
	state, err := readTaskDisposition(receipt.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "retained" || state.Guest == nil || *state.Guest != original || !state.Owned || state.TaskSucceeded {
		t.Fatalf("original ownership lost: %+v", state)
	}
	if data, err := os.ReadFile(filepath.Join(child, "operator")); err != nil || string(data) != "keep" {
		t.Fatalf("replacement changed %q %v", data, err)
	}
}

func TestWorkspaceOwnedForkRefusesReplacedSource(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(vmconfig.StateDirEnv, root)
	source := vmconfig.Path("base")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "linux-disk.img"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	expected, err := identifyTaskGuest(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, source+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "linux-disk.img"), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, err := forkOwnedWorkspaceGuest("base", "child", expected)
	if err == nil || !strings.Contains(err.Error(), "source identity changed before fork") || identity.Inode != 0 {
		t.Fatalf("accepted replaced source %+v %v", identity, err)
	}
	if _, err := os.Stat(vmconfig.Path("child")); !os.IsNotExist(err) {
		t.Fatalf("created child from replacement: %v", err)
	}
}

func TestProductionWorkspaceUsesOwnedFork(t *testing.T) {
	if defaultWorkspaceDeps().ForkOwned == nil {
		t.Fatal("production fork lacks recorded identity boundary")
	}
}
