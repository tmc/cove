package main

import (
	"context"
	"errors"
	"testing"
	"time"

	vz "github.com/tmc/apple/virtualization"
)

func TestGracefulWindowShutdown(t *testing.T) {
	tests := []struct {
		name        string
		initial     vz.VZVirtualMachineState
		next        vz.VZVirtualMachineState
		requestErr  error
		canceled    bool
		wantRequest bool
		wantErr     bool
	}{
		{name: "already stopped", initial: vz.VZVirtualMachineStateStopped},
		{name: "guest stops", initial: vz.VZVirtualMachineStateRunning, next: vz.VZVirtualMachineStateStopped, wantRequest: true},
		{name: "paused", initial: vz.VZVirtualMachineStatePaused, wantErr: true},
		{name: "request rejected", initial: vz.VZVirtualMachineStateRunning, requestErr: errors.New("rejected"), wantRequest: true, wantErr: true},
		{name: "timeout", initial: vz.VZVirtualMachineStateRunning, next: vz.VZVirtualMachineStateRunning, canceled: true, wantRequest: true, wantErr: true},
		{name: "VM error", initial: vz.VZVirtualMachineStateRunning, next: vz.VZVirtualMachineStateError, wantRequest: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			requested := false
			err := gracefulWindowShutdown(ctx, func() error { requested = true; return tt.requestErr }, func() (vz.VZVirtualMachineState, error) {
				if requested {
					return tt.next, nil
				}
				return tt.initial, nil
			})
			if requested != tt.wantRequest {
				t.Errorf("requested = %v, want %v", requested, tt.wantRequest)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

func TestWindowCloseShutdownFailureKeepsRuntime(t *testing.T) {
	orig := dispatchAsyncMainFn
	dispatchAsyncMainFn = func(fn func()) { fn() }
	defer func() { dispatchAsyncMainFn = orig }()
	done := make(chan struct{})
	c := &windowCloseController{
		shutDown:       func() error { return errors.New("guest busy") },
		shutdownFailed: func(error) { close(done) },
		setForceStop:   func() { t.Error("forced stop without confirmation") },
		doCleanup:      func() { t.Error("cleaned up running guest") },
	}
	c.HandleChoice(windowCloseShutDown)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown failure callback timed out")
	}
}

func TestWindowCloseShutdownWaitsBeforeCleanup(t *testing.T) {
	orig := dispatchAsyncMainFn
	dispatchAsyncMainFn = func(fn func()) { fn() }
	defer func() { dispatchAsyncMainFn = orig }()
	requested := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan struct{})
	c := &windowCloseController{
		shutDown: func() error { close(requested); <-finish; return nil },
		setForceStop: func() {
			select {
			case <-finish:
			default:
				t.Error("cleanup mode changed before guest stopped")
			}
		},
		doCleanup: func() {
			select {
			case <-finish:
			default:
				t.Error("cleanup before guest stopped")
			}
		},
		terminateApp: func() { close(done) },
	}
	c.HandleChoice(windowCloseShutDown)
	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown request timed out")
	}
	close(finish)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown cleanup timed out")
	}
}
