package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestExtractVMFlag(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantVM        string
		wantRemaining []string
	}{
		{
			name:          "no vm flag",
			args:          []string{"ls", "-la"},
			wantVM:        "",
			wantRemaining: []string{"ls", "-la"},
		},
		{
			name:          "-vm value at start",
			args:          []string{"-vm", "box1", "list"},
			wantVM:        "box1",
			wantRemaining: []string{"list"},
		},
		{
			name:          "--vm value at start",
			args:          []string{"--vm", "box1", "list"},
			wantVM:        "box1",
			wantRemaining: []string{"list"},
		},
		{
			name:          "-vm=value at start",
			args:          []string{"-vm=box1", "list"},
			wantVM:        "box1",
			wantRemaining: []string{"list"},
		},
		{
			name:          "--vm=value at start",
			args:          []string{"--vm=box1", "list"},
			wantVM:        "box1",
			wantRemaining: []string{"list"},
		},
		{
			name:          "-vm value at end",
			args:          []string{"list", "-vm", "box1"},
			wantVM:        "box1",
			wantRemaining: []string{"list"},
		},
		{
			name:          "exec with -vm at end",
			args:          []string{"ls", "-la", "-vm", "box1"},
			wantVM:        "box1",
			wantRemaining: []string{"ls", "-la"},
		},
		{
			name:          "exec with -vm at start",
			args:          []string{"-vm", "box1", "ls", "-la"},
			wantVM:        "box1",
			wantRemaining: []string{"ls", "-la"},
		},
		{
			name:          "stop scanning at double-dash",
			args:          []string{"ls", "--", "-vm", "box1"},
			wantVM:        "",
			wantRemaining: []string{"ls", "--", "-vm", "box1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVM, gotRemaining := extractVMFlag(tt.args)
			if gotVM != tt.wantVM {
				t.Errorf("extractVMFlag() vm = %q, want %q", gotVM, tt.wantVM)
			}
			if !reflect.DeepEqual(gotRemaining, tt.wantRemaining) {
				t.Errorf("extractVMFlag() remaining = %v, want %v", gotRemaining, tt.wantRemaining)
			}
		})
	}
}

func writeTestVM(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(vmconfig.BaseDir(), name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir vm dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aux.img"), []byte("aux"), 0644); err != nil {
		t.Fatalf("write aux: %v", err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err == nil {
		return realDir
	}
	return dir
}

func TestResolveTargetVM_Precedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	boxA := writeTestVM(t, "box-a")
	boxB := writeTestVM(t, "box-b")

	// 1. Explicit VM
	name, dir, err := resolveTargetVM(VMResolveOptions{
		Command:    "test",
		ExplicitVM: "box-a",
	})
	if err != nil {
		t.Fatalf("resolveTargetVM explicit error = %v", err)
	}
	if name != "box-a" || dir != boxA {
		t.Errorf("got (%q, %q), want (box-a, %q)", name, dir, boxA)
	}

	// 1b. Explicit VM that does not exist fails
	_, _, err = resolveTargetVM(VMResolveOptions{
		Command:    "test",
		ExplicitVM: "nonexistent",
	})
	if err == nil || !strings.Contains(err.Error(), "no VM named \"nonexistent\"") {
		t.Errorf("want not found error for nonexistent explicit VM, got %v", err)
	}

	// 2. Positional VM (non-optional)
	name, dir, err = resolveTargetVM(VMResolveOptions{
		Command:        "test",
		PositionalArgs: []string{"box-b"},
	})
	if err != nil {
		t.Fatalf("resolveTargetVM positional error = %v", err)
	}
	if name != "box-b" || dir != boxB {
		t.Errorf("got (%q, %q), want (box-b, %q)", name, dir, boxB)
	}

	// 2b. Positional VM optional (exec): known VM
	name, dir, err = resolveTargetVM(VMResolveOptions{
		Command:                "exec",
		PositionalArgs:         []string{"box-b", "uname", "-a"},
		PositionalIsOptionalVM: true,
	})
	if err != nil {
		t.Fatalf("resolveTargetVM optional known error = %v", err)
	}
	if name != "box-b" || dir != boxB {
		t.Errorf("got (%q, %q), want (box-b, %q)", name, dir, boxB)
	}

	// Set active VM to box-a
	if err := vmconfig.SetActive("box-a"); err != nil {
		t.Fatalf("SetActive error: %v", err)
	}

	// 3. Positional VM optional (exec): unknown positional (e.g. "ls") falls back to active VM
	name, dir, err = resolveTargetVM(VMResolveOptions{
		Command:                "exec",
		PositionalArgs:         []string{"ls", "-la"},
		PositionalIsOptionalVM: true,
	})
	if err != nil {
		t.Fatalf("resolveTargetVM optional unknown error = %v", err)
	}
	if name != "box-a" || dir != boxA {
		t.Errorf("got (%q, %q), want (box-a, %q)", name, dir, boxA)
	}

	// 4. No args falls back to active VM
	name, dir, err = resolveTargetVM(VMResolveOptions{
		Command: "snapshot",
	})
	if err != nil {
		t.Fatalf("resolveTargetVM active fallback error = %v", err)
	}
	if name != "box-a" || dir != boxA {
		t.Errorf("got (%q, %q), want (box-a, %q)", name, dir, boxA)
	}
}

func TestExecArgs_UnknownPositionalFallsBackToActiveVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTestVM(t, "my-active-vm")
	if err := vmconfig.SetActive("my-active-vm"); err != nil {
		t.Fatalf("SetActive error = %v", err)
	}

	// cove exec ls -la (without -vm) treats ls -la as command and falls back to active VM
	opts, vm, argv, err := parseExecArgsWithDefault([]string{"--", "ls", "-la"}, "")
	if err != nil {
		t.Fatalf("parseExecArgsWithDefault error = %v", err)
	}
	if vm != "my-active-vm" {
		t.Errorf("vm = %q, want my-active-vm", vm)
	}
	if !reflect.DeepEqual(argv, []string{"ls", "-la"}) {
		t.Errorf("argv = %v, want [ls, -la]", argv)
	}
	if opts.vm != "my-active-vm" {
		t.Errorf("opts.vm = %q, want my-active-vm", opts.vm)
	}

	// cove exec ls -la -vm custom
	writeTestVM(t, "custom")
	_, vm, argv, err = parseExecArgsWithDefault([]string{"-vm", "custom", "ls", "-la"}, "")
	if err != nil {
		t.Fatalf("parseExecArgsWithDefault with trailing -vm error = %v", err)
	}
	if vm != "custom" {
		t.Errorf("vm = %q, want custom", vm)
	}
	if !reflect.DeepEqual(argv, []string{"ls", "-la"}) {
		t.Errorf("argv = %v, want [ls, -la]", argv)
	}
}

func TestTypoVMDoesNotCreateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	typoName := "nonexistent-typo-vm"
	typoDir := filepath.Join(vmconfig.BaseDir(), typoName)
	typoBundle := filepath.Join(vmconfig.BundleDir(), typoName+".covevm")

	env := newCommandEnv()

	// snapshot -vm typo list must error and NOT create typo directory
	if err := handleSnapshotCommand(env, []string{"-vm", typoName, "list"}); err == nil {
		t.Error("handleSnapshotCommand on typo VM succeeded, want error")
	}
	if _, err := os.Stat(typoDir); !os.IsNotExist(err) {
		t.Errorf("typo VM dir was created at %s by snapshot", typoDir)
	}

	// disk-snapshot -vm typo list must error and NOT create typo directory
	if err := handleDiskSnapshotCommand([]string{"-vm", typoName, "list"}); err == nil {
		t.Error("handleDiskSnapshotCommand on typo VM succeeded, want error")
	}
	if _, err := os.Stat(typoDir); !os.IsNotExist(err) {
		t.Errorf("typo VM dir was created at %s by disk-snapshot", typoDir)
	}
	if _, err := os.Stat(typoBundle); !os.IsNotExist(err) {
		t.Errorf("typo VM bundle alias was created at %s", typoBundle)
	}
}

func TestSnapshotVMFlagAnywhere(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() { vmName, vmDir = oldVMName, oldVMDir })
	vmName, vmDir = "", ""
	writeTestVM(t, "snap-target")

	env := newCommandEnv()

	// cove snapshot -vm snap-target list
	if err := handleSnapshotCommand(env, []string{"-vm", "snap-target", "list"}); err != nil {
		t.Errorf("snapshot -vm target list error = %v", err)
	}

	// cove snapshot list -vm snap-target
	if err := handleSnapshotCommand(env, []string{"list", "-vm", "snap-target"}); err != nil {
		t.Errorf("snapshot list -vm target error = %v", err)
	}

	// cove disk-snapshot -vm snap-target list
	if err := handleDiskSnapshotCommand([]string{"-vm", "snap-target", "list"}); err != nil {
		t.Errorf("disk-snapshot -vm target list error = %v", err)
	}

	// cove disk-snapshot list -vm snap-target
	if err := handleDiskSnapshotCommand([]string{"list", "-vm", "snap-target"}); err != nil {
		t.Errorf("disk-snapshot list -vm target error = %v", err)
	}
}

func TestRMCascadeAfterPositional(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	writeTreeVM(t, "parent-node", vmconfig.Config{})
	writeTreeVM(t, "child-node", vmconfig.Config{ParentVM: "parent-node"})

	// Parse flags for vm delete with cascade after positional
	delFS := flag.NewFlagSet("vm delete", flag.ContinueOnError)
	var delYes bool
	delFS.BoolVar(&delYes, "y", false, "skip confirmation")
	delFS.BoolVar(&delYes, "yes", false, "skip confirmation")
	delCascade := delFS.Bool("cascade", false, "cascade")
	var delVM string
	delFS.StringVar(&delVM, "vm", "", "vm")

	args := moveKnownFlagsFirst([]string{"parent-node", "--cascade", "-y"}, map[string]bool{
		"y": false, "yes": false, "cascade": false, "vm": true,
	})
	if err := parseFlagsOrHelp(delFS, args); err != nil {
		t.Fatalf("parseFlagsOrHelp error = %v", err)
	}
	if !*delCascade {
		t.Fatalf("delCascade = false, want true")
	}
	if delFS.NArg() < 1 || delFS.Arg(0) != "parent-node" {
		t.Fatalf("delFS Arg(0) = %q, want parent-node", delFS.Arg(0))
	}

	// Deleting with cascade=true removes both
	if err := DeleteVMWithOptions(delFS.Arg(0), DeleteVMOptions{Cascade: *delCascade}); err != nil {
		t.Fatalf("DeleteVMWithOptions with cascade error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), "parent-node")); !os.IsNotExist(err) {
		t.Errorf("parent-node still exists")
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), "child-node")); !os.IsNotExist(err) {
		t.Errorf("child-node still exists")
	}
}

func TestShellVMResolution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() { vmName, vmDir = oldVMName, oldVMDir })
	vmName, vmDir = "", ""
	writeTestVM(t, "shell-box")
	if err := vmconfig.SetActive("shell-box"); err != nil {
		t.Fatalf("SetActive error = %v", err)
	}

	// cove shell -vm shell-box: parses -vm and resolves shell-box
	err := shellCommand([]string{"-vm", "shell-box"})
	if err == nil || (!strings.Contains(err.Error(), "shell-box") && !strings.Contains(err.Error(), "c.sock")) {
		t.Errorf("shellCommand(-vm shell-box) want socket/running error, got %v", err)
	}
	if strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("shellCommand failed with undefined flag: %v", err)
	}

	// cove shell (no args): falls back to active VM (shell-box)
	err = shellCommand([]string{})
	if err == nil || (!strings.Contains(err.Error(), "shell-box") && !strings.Contains(err.Error(), "c.sock")) {
		t.Errorf("shellCommand() want error mentioning shell-box, got %v", err)
	}

	// cove shell -vm nonexistent: fails with not found
	err = shellCommand([]string{"-vm", "nonexistent"})
	if err == nil || !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("shellCommand(-vm nonexistent) want not found, got %v", err)
	}
}
