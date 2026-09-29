package main

import (
	"sync/atomic"

	"github.com/tmc/apple/appkit"
	vz "github.com/tmc/apple/virtualization"
)

type windowCloseAction int

const (
	windowCloseCancel windowCloseAction = iota
	windowCloseKeepRunning
	windowCloseSuspend
	windowCloseShutDown
)

func (a windowCloseAction) String() string {
	switch a {
	case windowCloseCancel:
		return "cancel"
	case windowCloseKeepRunning:
		return "keep-running"
	case windowCloseSuspend:
		return "suspend"
	case windowCloseShutDown:
		return "shut-down"
	default:
		return "unknown"
	}
}

const windowClosePromptMessage = "Do you want to suspend the virtual machine, shut it down, or keep it running in the background?"

func vmRequiresCloseConfirmation(state vz.VZVirtualMachineState) bool {
	return state == vz.VZVirtualMachineStateRunning || state == vz.VZVirtualMachineStatePaused
}

func windowCloseButtonTitles(canSuspend bool) []string {
	if canSuspend {
		return []string{"Suspend", "Shut Down", "Keep Running", "Cancel"}
	}
	return []string{"Shut Down", "Keep Running", "Cancel"}
}

func windowCloseActionForResponse(response appkit.NSModalResponse, canSuspend bool) windowCloseAction {
	if canSuspend {
		switch response {
		case appkit.AlertFirstButtonReturn:
			return windowCloseSuspend
		case appkit.AlertSecondButtonReturn:
			return windowCloseShutDown
		case appkit.AlertThirdButtonReturn:
			return windowCloseKeepRunning
		case appkit.AlertThirdButtonReturn + 1:
			return windowCloseCancel
		default:
			return windowCloseCancel
		}
	}
	switch response {
	case appkit.AlertFirstButtonReturn:
		return windowCloseShutDown
	case appkit.AlertSecondButtonReturn:
		return windowCloseKeepRunning
	case appkit.AlertThirdButtonReturn:
		return windowCloseCancel
	default:
		return windowCloseCancel
	}
}

func newWindowCloseAlert(canSuspend bool) appkit.NSAlert {
	alert := appkit.NewNSAlert()
	alert.SetMessageText(windowClosePromptMessage)
	for _, title := range windowCloseButtonTitles(canSuspend) {
		alert.AddButtonWithTitle(title)
	}
	return alert
}

type windowCloseController struct {
	vmState        func() (vz.VZVirtualMachineState, error)
	canSuspend     func() bool
	showAlert      func(canSuspend bool, onChoice func(windowCloseAction))
	hideWindow     func()
	closeWindow    func()
	terminateApp   func()
	doCleanup      func()
	quitRuntime    func()
	cleanupDone    func() bool
	terminating    func() bool
	setTerminating func()
	setForceStop   func()
	replyTerminate func()
}

func (c *windowCloseController) ShouldClose() bool {
	if (c.cleanupDone != nil && c.cleanupDone()) || (c.terminating != nil && c.terminating()) {
		return true
	}
	state, err := c.vmState()
	if err != nil || !vmRequiresCloseConfirmation(state) {
		if c.quitRuntime != nil {
			c.quitRuntime()
		}
		return true
	}
	canSuspend := c.canSuspend != nil && c.canSuspend()
	if c.showAlert != nil {
		c.showAlert(canSuspend, c.HandleChoice)
	}
	return false
}

func (c *windowCloseController) HandleChoice(action windowCloseAction) {
	switch action {
	case windowCloseCancel:
	case windowCloseKeepRunning:
		if c.hideWindow != nil {
			c.hideWindow()
		}
	case windowCloseSuspend:
		if c.setTerminating != nil {
			c.setTerminating()
		}
		go func() {
			if c.doCleanup != nil {
				c.doCleanup()
			}
			DispatchAsyncMain(func() {
				if c.quitRuntime != nil {
					c.quitRuntime()
				}
				if c.replyTerminate != nil {
					c.replyTerminate()
				}
				if c.closeWindow != nil {
					c.closeWindow()
				}
				if c.terminateApp != nil {
					c.terminateApp()
				}
			})
		}()
	case windowCloseShutDown:
		if c.setForceStop != nil {
			c.setForceStop()
		}
		if c.setTerminating != nil {
			c.setTerminating()
		}
		go func() {
			if c.doCleanup != nil {
				c.doCleanup()
			}
			DispatchAsyncMain(func() {
				if c.quitRuntime != nil {
					c.quitRuntime()
				}
				if c.replyTerminate != nil {
					c.replyTerminate()
				}
				if c.closeWindow != nil {
					c.closeWindow()
				}
				if c.terminateApp != nil {
					c.terminateApp()
				}
			})
		}()
	}
}

type appTerminationCoordinator struct {
	cleanupDone          *atomic.Bool
	terminating          *atomic.Bool
	shouldTerminateReply *atomic.Bool
	beforeCleanup        func()
	doCleanup            func()
	replyTerminate       func()
	stopLoop             func()
}

func (c *appTerminationCoordinator) ShouldTerminate() appkit.NSApplicationTerminateReply {
	if c.cleanupDone != nil && c.cleanupDone.Load() {
		return appkit.NSTerminateNow
	}
	if c.shouldTerminateReply != nil {
		c.shouldTerminateReply.Store(true)
	}
	if c.terminating != nil && c.terminating.Swap(true) {
		return appkit.NSTerminateLater
	}
	if c.beforeCleanup != nil {
		c.beforeCleanup()
	}
	go func() {
		if c.doCleanup != nil {
			c.doCleanup()
		}
		DispatchAsyncMain(func() {
			if c.replyTerminate != nil {
				c.replyTerminate()
			}
			if c.stopLoop != nil {
				c.stopLoop()
			}
		})
	}()
	return appkit.NSTerminateLater
}
