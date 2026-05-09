//go:build darwin || linux

package main

import (
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"testing"
)

func TestSetUserLookupFailure(t *testing.T) {
	cmd := &exec.Cmd{}
	err := setUser(cmd, "this-user-should-not-exist-zzz-r185")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "lookup user") {
		t.Errorf("error %q missing lookup-user prefix", err)
	}
	if cmd.SysProcAttr != nil {
		t.Errorf("SysProcAttr should remain nil on lookup failure, got %+v", cmd.SysProcAttr)
	}
}

func TestSetUserSuccessSetsCredential(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skipf("user.Current(): %v", err)
	}
	cmd := &exec.Cmd{}
	if err := setUser(cmd, u.Username); err != nil {
		t.Fatalf("setUser(%q): %v", u.Username, err)
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil {
		t.Fatal("Credential not set on cmd.SysProcAttr")
	}
}

func TestEnsureSysProcAttrAllocatesAndPreserves(t *testing.T) {
	cmd := &exec.Cmd{}
	got := ensureSysProcAttr(cmd)
	if got == nil || cmd.SysProcAttr != got {
		t.Fatal("ensureSysProcAttr did not allocate and assign")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	again := ensureSysProcAttr(cmd)
	if again != cmd.SysProcAttr {
		t.Errorf("ensureSysProcAttr replaced existing SysProcAttr; want preservation")
	}
	if !again.Setpgid {
		t.Errorf("preserved SysProcAttr lost Setpgid=true")
	}
}

func TestConfigureProcessGroupSetsPgid(t *testing.T) {
	cmd := &exec.Cmd{}
	configureProcessGroup(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Errorf("configureProcessGroup did not set Setpgid=true")
	}
}
