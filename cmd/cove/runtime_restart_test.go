package main

import (
	"errors"
	"testing"
	"time"

	vz "github.com/tmc/apple/virtualization"
)

func TestStopDuringTransitionFailed(t *testing.T) {
	tests := []struct {
		name           string
		stopErr        error
		reachedStopped bool
		want           bool
	}{
		{"clean stop", nil, true, false},
		{"spurious error but stopped", errors.New("Internal Virtualization error. The virtual machine stopped unexpectedly."), true, false},
		{"error and still running", errors.New("boom"), false, true},
		{"no error but never stopped", nil, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stopDuringTransitionFailed(tt.stopErr, tt.reachedStopped); got != tt.want {
				t.Errorf("stopDuringTransitionFailed(%v, %v) = %v, want %v",
					tt.stopErr, tt.reachedStopped, got, tt.want)
			}
		})
	}
}

func TestWaitForVMStatePollReachesState(t *testing.T) {
	calls := 0
	got, err := waitForVMStatePoll(func() (vz.VZVirtualMachineState, error) {
		calls++
		if calls < 3 {
			return vz.VZVirtualMachineStateStopping, nil
		}
		return vz.VZVirtualMachineStateStopped, nil
	}, vz.VZVirtualMachineStateStopped, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("waitForVMStatePoll: %v", err)
	}
	if got != vz.VZVirtualMachineStateStopped {
		t.Fatalf("state = %v, want stopped", got)
	}
}

func TestWaitForVMStatePollTimesOut(t *testing.T) {
	_, err := waitForVMStatePoll(func() (vz.VZVirtualMachineState, error) {
		return vz.VZVirtualMachineStateRunning, nil
	}, vz.VZVirtualMachineStateStopped, 5*time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatal("waitForVMStatePoll succeeded, want timeout")
	}
}

func TestWaitForVMStatePollTolerantOfPollErrors(t *testing.T) {
	calls := 0
	if _, err := waitForVMStatePoll(func() (vz.VZVirtualMachineState, error) {
		calls++
		if calls < 3 {
			return 0, errors.New("timed out checking VM state")
		}
		return vz.VZVirtualMachineStateStopped, nil
	}, vz.VZVirtualMachineStateStopped, time.Second, time.Millisecond); err != nil {
		t.Fatalf("waitForVMStatePoll: %v", err)
	}
}

func TestVMBootTransitionInProgress(t *testing.T) {
	if vmBootTransitionInProgress() {
		t.Fatal("boot transition in progress before any began")
	}
	beginVMBootTransition()
	beginVMBootTransition()
	if !vmBootTransitionInProgress() {
		t.Fatal("boot transition not reported after begin")
	}
	endVMBootTransition()
	if !vmBootTransitionInProgress() {
		t.Fatal("boot transition ended while one is still in flight")
	}
	endVMBootTransition()
	if vmBootTransitionInProgress() {
		t.Fatal("boot transition still reported after all ended")
	}
}
