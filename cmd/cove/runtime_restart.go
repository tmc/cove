package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
)

const (
	// restartGracefulStopTimeout bounds how long the guest is given to act on
	// an ACPI shutdown request before the VM is powered off.
	restartGracefulStopTimeout = 45 * time.Second
	// restartHardStopTimeout bounds the wait for the stopped state after a
	// hard power off.
	restartHardStopTimeout = 20 * time.Second
	// restartStartTimeout bounds the wait for the running state after the
	// restart's start call.
	restartStartTimeout = 60 * time.Second

	restartStatePollInterval = 250 * time.Millisecond
)

// vmBootTransitions counts in-flight restart or recovery-boot transitions.
//
// The run-loop state monitors treat any observation of the stopped state as
// the VM having exited and tear the app down ("VM stopped"). The stop half of
// a restart is exactly that observation, so a transition in flight must
// suppress it; otherwise a restart races the monitor and the app quits before
// the VM is started again.
var vmBootTransitions atomic.Int64

func beginVMBootTransition() { vmBootTransitions.Add(1) }
func endVMBootTransition()   { vmBootTransitions.Add(-1) }

// vmBootTransitionInProgress reports whether a restart or recovery boot is
// currently stopping and restarting the VM.
func vmBootTransitionInProgress() bool { return vmBootTransitions.Load() > 0 }

// stopDuringTransitionFailed reports whether a stop completion-handler error
// is a genuine failure.
//
// stopWithCompletionHandler: is a hard power off. Cutting power to a live
// guest routinely completes with an "Internal Virtualization error. The
// virtual machine stopped unexpectedly." — the framework reports how the guest
// went down, not whether the stop worked. The reported error is therefore only
// conclusive when the VM did not reach the stopped state.
func stopDuringTransitionFailed(stopErr error, reachedStopped bool) bool {
	return stopErr != nil && !reachedStopped
}

// waitForVMStatePoll polls until the VM reports want, returning the last state
// observed. Poll errors are tolerated until the timeout elapses.
func waitForVMStatePoll(poll func() (vz.VZVirtualMachineState, error), want vz.VZVirtualMachineState, timeout, every time.Duration) (vz.VZVirtualMachineState, error) {
	if every <= 0 {
		every = restartStatePollInterval
	}
	deadline := time.Now().Add(timeout)
	last := vz.VZVirtualMachineState(-1)
	var lastErr error
	for {
		state, err := poll()
		if err == nil {
			last, lastErr = state, nil
			if state == want {
				return state, nil
			}
		} else {
			lastErr = err
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(every)
	}
	if lastErr != nil {
		return last, fmt.Errorf("timed out waiting for VM state %s: %v", vmStateName(want), lastErr)
	}
	return last, fmt.Errorf("timed out waiting for VM state %s (last state: %s)", vmStateName(want), vmStateName(last))
}

// requestGracefulVMStop sends an ACPI shutdown request the guest can act on.
// It reports whether the request was accepted.
func requestGracefulVMStop(vm vz.VZVirtualMachine, queue dispatch.Queue) bool {
	type result struct {
		ok  bool
		err error
	}
	ch := make(chan result, 1)
	DispatchAsyncQueue(queue, func() {
		if !vm.CanRequestStop() {
			ch <- result{}
			return
		}
		ok, err := vm.RequestStopWithError()
		ch <- result{ok: ok, err: snapshotNSError(err)}
	})
	select {
	case r := <-ch:
		if r.err != nil {
			if verbose {
				fmt.Printf("graceful stop request failed: %v\n", r.err)
			}
			return false
		}
		return r.ok
	case <-time.After(10 * time.Second):
		return false
	}
}

// hardStopVMAndWait powers the VM off and returns the completion handler's
// error, if any. The error is not conclusive; callers must check the state.
func hardStopVMAndWait(vm vz.VZVirtualMachine, queue dispatch.Queue) error {
	ch := make(chan error, 1)
	DispatchAsyncQueue(queue, func() {
		if !vm.CanStop() {
			ch <- nil
			return
		}
		vm.StopWithCompletionHandler(func(err error) {
			err = snapshotNSError(err)
			if isVZCannotStopError(err) {
				err = nil
			}
			ch <- err
		})
	})
	select {
	case err := <-ch:
		return err
	case <-time.After(restartHardStopTimeout):
		return fmt.Errorf("stop timed out")
	}
}

// stopVMForBootTransition stops the VM ahead of a restart or recovery boot: it
// asks the guest to shut down, falls back to a hard power off, and returns nil
// once the VM is observed stopped regardless of any error reported along the
// way.
func stopVMForBootTransition(label string, vm vz.VZVirtualMachine, queue dispatch.Queue) error {
	poll := func() (vz.VZVirtualMachineState, error) { return currentVMState(vm, queue) }

	state, err := poll()
	if err != nil {
		return fmt.Errorf("read vm state: %w", err)
	}
	if state == vz.VZVirtualMachineStateStopped {
		return nil
	}

	if requestGracefulVMStop(vm, queue) {
		fmt.Printf("%s: asked the guest to shut down...\n", label)
		if _, err := waitForVMStatePoll(poll, vz.VZVirtualMachineStateStopped, restartGracefulStopTimeout, restartStatePollInterval); err == nil {
			return nil
		}
		fmt.Printf("%s: guest did not shut down in time, powering off...\n", label)
	}

	stopErr := hardStopVMAndWait(vm, queue)
	final, waitErr := waitForVMStatePoll(poll, vz.VZVirtualMachineStateStopped, restartHardStopTimeout, restartStatePollInterval)
	reachedStopped := final == vz.VZVirtualMachineStateStopped
	if reachedStopped {
		if stopErr != nil && verbose {
			fmt.Printf("%s: VM stopped; ignoring error reported by the stop handler: %v\n", label, stopErr)
		}
		return nil
	}
	if stopDuringTransitionFailed(stopErr, reachedStopped) {
		return stopErr
	}
	return waitErr
}

// startVMAfterStop starts a stopped VM and waits for it to reach the running
// state. starter performs the framework call on the VM queue.
func startVMAfterStop(vm vz.VZVirtualMachine, queue dispatch.Queue, starter func(handler func(error))) error {
	ch := make(chan error, 1)
	DispatchAsyncQueue(queue, func() {
		if state := vz.VZVirtualMachineState(vm.State()); state != vz.VZVirtualMachineStateStopped {
			ch <- fmt.Errorf("vm is %s, not stopped", vmStateName(state))
			return
		}
		starter(func(err error) { ch <- snapshotNSError(err) })
	})
	select {
	case err := <-ch:
		if err != nil {
			return err
		}
	case <-time.After(restartStartTimeout):
		return fmt.Errorf("start timed out")
	}
	if _, err := waitForVMStatePoll(func() (vz.VZVirtualMachineState, error) { return currentVMState(vm, queue) },
		vz.VZVirtualMachineStateRunning, restartStartTimeout, restartStatePollInterval); err != nil {
		return err
	}
	return nil
}

// reportBootTransitionFailure prints an actionable message when a restart or
// recovery boot leaves the VM off.
func reportBootTransitionFailure(label, phase string, vm vz.VZVirtualMachine, queue dispatch.Queue, err error) {
	fmt.Fprintf(os.Stderr, "error: %s: %v\n", phase, err)
	state, stateErr := currentVMState(vm, queue)
	if stateErr != nil {
		fmt.Fprintf(os.Stderr, "%s: VM state unavailable (%v); run `cove run` again to bring it back up\n", label, stateErr)
		return
	}
	fmt.Fprintf(os.Stderr, "%s: VM is %s; use the VM menu to start it again, or quit and run `cove run`\n",
		label, vmStateName(state))
}
