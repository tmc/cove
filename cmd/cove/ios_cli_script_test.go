package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
	"rsc.io/script"
)

func TestIOSNewScript(t *testing.T) {
	if !subcommandSkipsVMDir([]string{"ios", "new", "phone"}) {
		t.Fatal("ios command creates default vm directory")
	}
	root := t.TempDir()
	t.Setenv("COVE_STATE_DIR", filepath.Join(root, "state"))
	state, err := script.NewState(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Setenv("JOURNAL", filepath.Join(root, "mounts")); err != nil {
		t.Fatal(err)
	}
	cmds := script.DefaultCmds()
	cmds["ios"] = script.Command(script.CmdUsage{Summary: "run ios command"}, func(s *script.State, args ...string) (script.WaitFunc, error) {
		return func(s *script.State) (string, string, error) {
			var out, stderr bytes.Buffer
			code := runIOSCommand(commandEnv{Stdout: &out, Stderr: &stderr}, "ios", args)
			var err error
			if code != 0 {
				err = fmt.Errorf("exit %d", code)
			}
			return out.String(), stderr.String(), err
		}, nil
	})
	engine := &script.Engine{Cmds: cmds, Conds: script.DefaultConds()}
	var log bytes.Buffer
	source := `
! ios firmware _mount
stderr 'mount journal and operation are required'
! ios firmware _mount -journal $JOURNAL remount /dev/disk1 /Volumes/Other
stderr 'mount point is not owned by this journal'
! ios firmware prepare
stderr 'source, iphone, cloudos and output paths are required'
! ios firmware unknown
stderr 'usage: cove ios firmware prepare'

ios new -cpu 2 -memory 2 -disk 1 phone
stdout 'phone.covevm'
exists state/vms/phone.covevm/disk.img
exists state/vms/phone.covevm/sep.img
cp state/vms/phone.covevm/config.json before.json
! ios new -cpu 2 -memory 2 -disk 2 phone
stderr 'already exists'
cmp before.json state/vms/phone.covevm/config.json
! ios new ../escape
! exists state/escape.covevm
! ios new -disk 0 invalid
! exists state/vms/invalid.covevm
! ios run -debug-port 65536 phone
stderr 'invalid ios debug port'
! ios run -timeout -1s phone
stderr 'invalid ios debug port or timeout'
! ios run missing
stderr 'vm not found'
! exists state/vms/missing.covevm
ios config phone -network none -scale 2
stdout '"network":"none"'
stdout '"scale":2'
ios config phone
stdout '"network":"none"'
cp state/vms/phone.covevm/config.json configured.json
! ios config phone -scale 0
cmp configured.json state/vms/phone.covevm/config.json
`
	if err := engine.Execute(state, "ios-new.txtar", bufio.NewReader(strings.NewReader(source)), &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
}

func TestIOSConfigRejectsHeldRunLock(t *testing.T) {
	t.Setenv("COVE_STATE_DIR", t.TempDir())
	var out, stderr bytes.Buffer
	env := commandEnv{Stdout: &out, Stderr: &stderr}
	if code := runIOSNew(env, []string{"-cpu", "2", "-memory", "2", "-disk", "1", "phone"}); code != 0 {
		t.Fatalf("new=%d %s", code, stderr.String())
	}
	dir := vmconfig.Path("phone")
	path := filepath.Join(dir, "config.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireRunLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if code := runIOSConfig(env, []string{"phone", "-network", "none"}); code == 0 {
		t.Fatal("configured locked bundle")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("changed locked bundle")
	}
}
