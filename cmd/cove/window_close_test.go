package main

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tmc/apple/appkit"
	vz "github.com/tmc/apple/virtualization"
)

func TestVMRequiresCloseConfirmation(t *testing.T) {
	tests := []struct {
		name  string
		state vz.VZVirtualMachineState
		want  bool
	}{
		{name: "running", state: vz.VZVirtualMachineStateRunning, want: true},
		{name: "paused", state: vz.VZVirtualMachineStatePaused, want: true},
		{name: "stopped", state: vz.VZVirtualMachineStateStopped, want: false},
		{name: "error", state: vz.VZVirtualMachineStateError, want: false},
		{name: "starting", state: vz.VZVirtualMachineStateStarting, want: false},
		{name: "stopping", state: vz.VZVirtualMachineStateStopping, want: false},
		{name: "saving", state: vz.VZVirtualMachineStateSaving, want: false},
		{name: "restoring", state: vz.VZVirtualMachineStateRestoring, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vmRequiresCloseConfirmation(tt.state); got != tt.want {
				t.Errorf("vmRequiresCloseConfirmation(%v) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestWindowCloseButtonTitles(t *testing.T) {
	tests := []struct {
		name       string
		canSuspend bool
		want       []string
	}{
		{
			name:       "suspend supported",
			canSuspend: true,
			want:       []string{"Suspend", "Shut Down", "Keep Running", "Cancel"},
		},
		{
			name:       "suspend not supported",
			canSuspend: false,
			want:       []string{"Shut Down", "Keep Running", "Cancel"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := windowCloseButtonTitles(tt.canSuspend)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("windowCloseButtonTitles(%v) = %v, want %v", tt.canSuspend, got, tt.want)
			}
		})
	}
}

func TestWindowCloseActionForResponse(t *testing.T) {
	tests := []struct {
		name       string
		response   appkit.NSModalResponse
		canSuspend bool
		want       windowCloseAction
	}{
		// Suspend supported
		{name: "suspend supported - first button is suspend", response: appkit.AlertFirstButtonReturn, canSuspend: true, want: windowCloseSuspend},
		{name: "suspend supported - second button is shut down", response: appkit.AlertSecondButtonReturn, canSuspend: true, want: windowCloseShutDown},
		{name: "suspend supported - third button is keep running", response: appkit.AlertThirdButtonReturn, canSuspend: true, want: windowCloseKeepRunning},
		{name: "suspend supported - fourth button is cancel", response: appkit.AlertThirdButtonReturn + 1, canSuspend: true, want: windowCloseCancel},
		{name: "suspend supported - unknown response defaults to cancel", response: 9999, canSuspend: true, want: windowCloseCancel},

		// Suspend not supported
		{name: "suspend unsupported - first button is shut down", response: appkit.AlertFirstButtonReturn, canSuspend: false, want: windowCloseShutDown},
		{name: "suspend unsupported - second button is keep running", response: appkit.AlertSecondButtonReturn, canSuspend: false, want: windowCloseKeepRunning},
		{name: "suspend unsupported - third button is cancel", response: appkit.AlertThirdButtonReturn, canSuspend: false, want: windowCloseCancel},
		{name: "suspend unsupported - unknown response defaults to cancel", response: 9999, canSuspend: false, want: windowCloseCancel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowCloseActionForResponse(tt.response, tt.canSuspend); got != tt.want {
				t.Errorf("windowCloseActionForResponse(%v, %v) = %v, want %v", tt.response, tt.canSuspend, got, tt.want)
			}
		})
	}
}

func TestWindowCloseActionString(t *testing.T) {
	tests := []struct {
		action windowCloseAction
		want   string
	}{
		{windowCloseCancel, "cancel"},
		{windowCloseKeepRunning, "keep-running"},
		{windowCloseSuspend, "suspend"},
		{windowCloseShutDown, "shut-down"},
		{windowCloseAction(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.action.String(); got != tt.want {
			t.Errorf("windowCloseAction(%d).String() = %q, want %q", tt.action, got, tt.want)
		}
	}
}

func TestWindowCloseController_ShouldClose(t *testing.T) {
	t.Run("cleanup already done returns true", func(t *testing.T) {
		c := &windowCloseController{
			cleanupDone: func() bool { return true },
		}
		if got := c.ShouldClose(); !got {
			t.Errorf("ShouldClose() = false, want true")
		}
	})

	t.Run("terminating returns true", func(t *testing.T) {
		c := &windowCloseController{
			terminating: func() bool { return true },
		}
		if got := c.ShouldClose(); !got {
			t.Errorf("ShouldClose() = false, want true")
		}
	})

	t.Run("vm stopped calls quitRuntime and returns true", func(t *testing.T) {
		var quitCalled bool
		c := &windowCloseController{
			vmState: func() (vz.VZVirtualMachineState, error) {
				return vz.VZVirtualMachineStateStopped, nil
			},
			quitRuntime: func() {
				quitCalled = true
			},
		}
		if got := c.ShouldClose(); !got {
			t.Errorf("ShouldClose() = false, want true")
		}
		if !quitCalled {
			t.Errorf("quitRuntime was not called")
		}
	})

	t.Run("vm state error calls quitRuntime and returns true", func(t *testing.T) {
		var quitCalled bool
		c := &windowCloseController{
			vmState: func() (vz.VZVirtualMachineState, error) {
				return 0, errors.New("cannot read state")
			},
			quitRuntime: func() {
				quitCalled = true
			},
		}
		if got := c.ShouldClose(); !got {
			t.Errorf("ShouldClose() = false, want true")
		}
		if !quitCalled {
			t.Errorf("quitRuntime was not called")
		}
	})

	t.Run("vm running triggers alert and returns false", func(t *testing.T) {
		var alertShown bool
		var alertCanSuspend bool
		c := &windowCloseController{
			vmState: func() (vz.VZVirtualMachineState, error) {
				return vz.VZVirtualMachineStateRunning, nil
			},
			canSuspend: func() bool { return true },
			showAlert: func(canSuspend bool, _ func(windowCloseAction)) {
				alertShown = true
				alertCanSuspend = canSuspend
			},
		}
		if got := c.ShouldClose(); got {
			t.Errorf("ShouldClose() = true, want false")
		}
		if !alertShown {
			t.Errorf("showAlert was not called")
		}
		if !alertCanSuspend {
			t.Errorf("showAlert canSuspend = false, want true")
		}
	})
}

func TestWindowCloseController_HandleChoice(t *testing.T) {
	origDispatch := dispatchAsyncMainFn
	defer func() { dispatchAsyncMainFn = origDispatch }()
	dispatchAsyncMainFn = func(fn func()) { fn() }

	t.Run("cancel does nothing", func(t *testing.T) {
		var hideCalled, termCalled, cleanupCalled bool
		c := &windowCloseController{
			hideWindow:   func() { hideCalled = true },
			terminateApp: func() { termCalled = true },
			doCleanup:    func() { cleanupCalled = true },
		}
		c.HandleChoice(windowCloseCancel)
		if hideCalled || termCalled || cleanupCalled {
			t.Errorf("unexpected action called on cancel: hide=%v term=%v cleanup=%v", hideCalled, termCalled, cleanupCalled)
		}
	})

	t.Run("keep running hides window", func(t *testing.T) {
		var hideCalled, termCalled, cleanupCalled bool
		c := &windowCloseController{
			hideWindow:   func() { hideCalled = true },
			terminateApp: func() { termCalled = true },
			doCleanup:    func() { cleanupCalled = true },
		}
		c.HandleChoice(windowCloseKeepRunning)
		if !hideCalled {
			t.Errorf("hideWindow was not called")
		}
		if termCalled || cleanupCalled {
			t.Errorf("unexpected action called on keep running: term=%v cleanup=%v", termCalled, cleanupCalled)
		}
	})

	t.Run("suspend executes async cleanup and terminates", func(t *testing.T) {
		var (
			mu             sync.Mutex
			terminatingSet bool
			cleanupCalled  bool
			quitCalled     bool
			closeCalled    bool
			termCalled     bool
			doneCh         = make(chan struct{})
		)
		c := &windowCloseController{
			setTerminating: func() {
				mu.Lock()
				terminatingSet = true
				mu.Unlock()
			},
			doCleanup: func() {
				mu.Lock()
				cleanupCalled = true
				mu.Unlock()
			},
			quitRuntime: func() {
				mu.Lock()
				quitCalled = true
				mu.Unlock()
			},
			closeWindow: func() {
				mu.Lock()
				closeCalled = true
				mu.Unlock()
			},
			terminateApp: func() {
				mu.Lock()
				termCalled = true
				mu.Unlock()
				close(doneCh)
			},
		}
		c.HandleChoice(windowCloseSuspend)

		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for suspend cleanup completion")
		}

		mu.Lock()
		defer mu.Unlock()
		if !terminatingSet {
			t.Errorf("setTerminating was not called")
		}
		if !cleanupCalled {
			t.Errorf("doCleanup was not called")
		}
		if !quitCalled {
			t.Errorf("quitRuntime was not called")
		}
		if !closeCalled {
			t.Errorf("closeWindow was not called")
		}
		if !termCalled {
			t.Errorf("terminateApp was not called")
		}
	})

	t.Run("shutdown executes force stop and terminates", func(t *testing.T) {
		var (
			mu             sync.Mutex
			forceStopSet   bool
			terminatingSet bool
			cleanupCalled  bool
			quitCalled     bool
			closeCalled    bool
			termCalled     bool
			doneCh         = make(chan struct{})
		)
		c := &windowCloseController{
			setForceStop: func() {
				mu.Lock()
				forceStopSet = true
				mu.Unlock()
			},
			setTerminating: func() {
				mu.Lock()
				terminatingSet = true
				mu.Unlock()
			},
			doCleanup: func() {
				mu.Lock()
				cleanupCalled = true
				mu.Unlock()
			},
			quitRuntime: func() {
				mu.Lock()
				quitCalled = true
				mu.Unlock()
			},
			closeWindow: func() {
				mu.Lock()
				closeCalled = true
				mu.Unlock()
			},
			terminateApp: func() {
				mu.Lock()
				termCalled = true
				mu.Unlock()
				close(doneCh)
			},
		}
		c.HandleChoice(windowCloseShutDown)

		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for shutdown cleanup completion")
		}

		mu.Lock()
		defer mu.Unlock()
		if !forceStopSet {
			t.Errorf("setForceStop was not called")
		}
		if !terminatingSet {
			t.Errorf("setTerminating was not called")
		}
		if !cleanupCalled {
			t.Errorf("doCleanup was not called")
		}
		if !quitCalled {
			t.Errorf("quitRuntime was not called")
		}
		if !closeCalled {
			t.Errorf("closeWindow was not called")
		}
		if !termCalled {
			t.Errorf("terminateApp was not called")
		}
	})
}

func TestAppTerminationCoordinator(t *testing.T) {
	origDispatch := dispatchAsyncMainFn
	defer func() { dispatchAsyncMainFn = origDispatch }()
	dispatchAsyncMainFn = func(fn func()) { fn() }

	t.Run("returns NSTerminateNow when cleanup is already done", func(t *testing.T) {
		var cleanupDone atomic.Bool
		cleanupDone.Store(true)

		coordinator := &appTerminationCoordinator{
			cleanupDone: &cleanupDone,
		}
		reply := coordinator.ShouldTerminate()
		if reply != appkit.NSTerminateNow {
			t.Errorf("ShouldTerminate() = %v, want NSTerminateNow (%v)", reply, appkit.NSTerminateNow)
		}
	})

	t.Run("returns NSTerminateLater and executes cleanup", func(t *testing.T) {
		var (
			cleanupDone          atomic.Bool
			terminating          atomic.Bool
			shouldTerminateReply atomic.Bool
			mu                   sync.Mutex
			beforeCleanupCalled  bool
			doCleanupCalled      bool
			replyTerminateCalled bool
			stopLoopCalled       bool
			doneCh               = make(chan struct{})
		)

		coordinator := &appTerminationCoordinator{
			cleanupDone:          &cleanupDone,
			terminating:          &terminating,
			shouldTerminateReply: &shouldTerminateReply,
			beforeCleanup: func() {
				mu.Lock()
				beforeCleanupCalled = true
				mu.Unlock()
			},
			doCleanup: func() {
				mu.Lock()
				doCleanupCalled = true
				mu.Unlock()
			},
			replyTerminate: func() {
				mu.Lock()
				replyTerminateCalled = true
				mu.Unlock()
			},
			stopLoop: func() {
				mu.Lock()
				stopLoopCalled = true
				mu.Unlock()
				close(doneCh)
			},
		}

		reply := coordinator.ShouldTerminate()
		if reply != appkit.NSTerminateLater {
			t.Fatalf("ShouldTerminate() = %v, want NSTerminateLater (%v)", reply, appkit.NSTerminateLater)
		}

		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for async termination cleanup")
		}

		mu.Lock()
		defer mu.Unlock()
		if !beforeCleanupCalled {
			t.Errorf("beforeCleanup was not called")
		}
		if !doCleanupCalled {
			t.Errorf("doCleanup was not called")
		}
		if !replyTerminateCalled {
			t.Errorf("replyTerminate was not called")
		}
		if !stopLoopCalled {
			t.Errorf("stopLoop was not called")
		}
		if !shouldTerminateReply.Load() {
			t.Errorf("shouldTerminateReply was not set")
		}
	})

	t.Run("returns NSTerminateLater without duplicate cleanup if already terminating", func(t *testing.T) {
		var (
			cleanupDone          atomic.Bool
			terminating          atomic.Bool
			shouldTerminateReply atomic.Bool
			cleanupCalls         atomic.Int32
		)
		terminating.Store(true)

		coordinator := &appTerminationCoordinator{
			cleanupDone:          &cleanupDone,
			terminating:          &terminating,
			shouldTerminateReply: &shouldTerminateReply,
			doCleanup: func() {
				cleanupCalls.Add(1)
			},
		}

		reply := coordinator.ShouldTerminate()
		if reply != appkit.NSTerminateLater {
			t.Errorf("ShouldTerminate() = %v, want NSTerminateLater (%v)", reply, appkit.NSTerminateLater)
		}
		if cleanupCalls.Load() != 0 {
			t.Errorf("doCleanup was called %d times, want 0", cleanupCalls.Load())
		}
	})
}
