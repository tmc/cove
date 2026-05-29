package main

import (
	"strings"
	"testing"
	"time"

	vz "github.com/tmc/apple/virtualization"
)

func TestWaitForVMStartPollTimeoutIncludesDiagnostics(t *testing.T) {
	startErr := make(chan error)
	err := waitForVMStartPoll(startErr, func() (vz.VZVirtualMachineState, error) {
		return vz.VZVirtualMachineStateStarting, nil
	}, vmStartWaitOptions{
		Timeout:   5 * time.Millisecond,
		PollEvery: time.Millisecond,
		Diagnostics: vmStartDiagnostics{
			BootMode: "normal",
			Headless: true,
		},
	})
	if err == nil {
		t.Fatal("waitForVMStartPoll succeeded, want timeout")
	}
	msg := err.Error()
	for _, want := range []string{"VM start timeout", "last state: starting", "boot_mode=normal", "headless=true"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("timeout error %q missing %q", msg, want)
		}
	}
}

func TestWaitForVMStartPollReturnsRunning(t *testing.T) {
	startErr := make(chan error)
	err := waitForVMStartPoll(startErr, func() (vz.VZVirtualMachineState, error) {
		return vz.VZVirtualMachineStateRunning, nil
	}, vmStartWaitOptions{
		Timeout:   time.Second,
		PollEvery: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("waitForVMStartPoll: %v", err)
	}
}
