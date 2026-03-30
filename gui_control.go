package main

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

const (
	headlessOffscreenX = -32000
	headlessOffscreenY = -32000
)

type GUIStatus struct {
	Supported         bool   `json:"supported"`
	Headed            bool   `json:"headed"`
	WindowReady       bool   `json:"window_ready"`
	CaptureMode       string `json:"capture_mode"`
	AutomationBackend string `json:"automation_backend"`
	CaptureBackend    string `json:"capture_backend"`
	InputBackend      string `json:"input_backend"`
	KeyboardMode      string `json:"keyboard_mode"`
	MouseMode         string `json:"mouse_mode"`
}

type VMGUIController interface {
	Open() error
	Close() error
	Status() GUIStatus
	Shutdown()
}

type vmGUIController struct {
	app             appkit.NSApplication
	vm              vz.VZVirtualMachine
	vmQueue         dispatch.Queue
	control         *ControlServer
	vmDirectory     string
	windowTitleBase string

	mu               sync.Mutex
	window           appkit.NSWindow
	vmView           vz.VZVirtualMachineView
	toolbar          *VMToolbar
	frameAutosave    appkit.NSWindowFrameAutosaveName
	lastVisibleFrame corefoundation.CGRect
	headed           bool
	menuConfigured   bool
	shuttingDown     bool

	windowDelegate appkit.NSWindowDelegateObject
	appDelegate    appkit.NSApplicationDelegateObject
}

func ensureAppReady(policy appkit.NSApplicationActivationPolicy) appkit.NSApplication {
	app := getSharedApp()
	app.SetActivationPolicy(policy)
	if policy == appkit.NSApplicationActivationPolicyRegular {
		setAppIcon(&app)
	}
	if !appFinishedLaunching {
		foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(0, false, func(_ *foundation.NSTimer) {
			app.Stop(nil)
			postDummyEvent(app)
		})
		app.Run()
		appFinishedLaunching = true
	}
	return app
}

func newHeadlessGUIController(app appkit.NSApplication, vm vz.VZVirtualMachine, queue dispatch.Queue, control *ControlServer, initiallyHeaded bool) (*vmGUIController, error) {
	c := &vmGUIController{
		app:         app,
		vm:          vm,
		vmQueue:     queue,
		control:     control,
		vmDirectory: vmDir,
		headed:      initiallyHeaded,
	}
	c.windowTitleBase = controllerWindowTitle()
	if err := c.initWindow(); err != nil {
		return nil, err
	}
	return c, nil
}

func controllerWindowTitle() string {
	osLabel := "macOS VM"
	if linuxMode {
		osLabel = "Linux VM"
	}
	if vmName == "" || vmName == "default" {
		return osLabel
	}
	return fmt.Sprintf("%s — %s", osLabel, vmName)
}

func (c *vmGUIController) initWindow() error {
	vmView := vz.NewVZVirtualMachineView()
	vmView.SetVirtualMachine(&c.vm)
	vmView.SetCapturesSystemKeys(false)
	vmView.SetAutomaticallyReconfiguresDisplay(true)

	contentRect := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 100, Y: 100},
		Size:   corefoundation.CGSize{Width: defaultWindowWidth, Height: defaultWindowHeight},
	}
	window := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
		contentRect,
		appkit.NSWindowStyleMaskTitled|
			appkit.NSWindowStyleMaskClosable|
			appkit.NSWindowStyleMaskMiniaturizable|
			appkit.NSWindowStyleMaskResizable,
		appkit.NSBackingStoreBuffered,
		false,
	)
	window.SetStyleMask(
		appkit.NSWindowStyleMaskTitled |
			appkit.NSWindowStyleMaskClosable |
			appkit.NSWindowStyleMaskMiniaturizable |
			appkit.NSWindowStyleMaskResizable,
	)
	window.SetTitleVisibility(appkit.NSWindowTitleVisible)
	window.SetTitlebarAppearsTransparent(false)
	window.SetTitle(c.windowTitleBase)
	window.SetReleasedWhenClosed(false)

	restoredFrame, frameAutosaveName := configureWindowFramePersistence(window)
	vmViewAsNSView(vmView).SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{},
		Size:   contentRect.Size,
	})
	window.SetContentView(vmViewAsNSView(vmView))
	if !restoredFrame {
		window.Center()
	}
	c.window = window
	c.vmView = vmView
	c.frameAutosave = frameAutosaveName
	c.lastVisibleFrame = window.Frame()

	procName := "vz-macos"
	if vmName != "" && vmName != "default" {
		procName = fmt.Sprintf("vz-macos (%s)", vmName)
	}
	foundation.GetProcessInfoClass().ProcessInfo().SetProcessName(procName)
	if vmName != "" && vmName != "default" {
		dockTile := c.app.DockTile()
		dockTile.SetBadgeLabel(vmName)
	}

	c.windowDelegate = appkit.NewNSWindowDelegate(appkit.NSWindowDelegateConfig{
		ShouldClose: func(_ appkit.NSWindow) bool {
			c.mu.Lock()
			shuttingDown := c.shuttingDown
			c.mu.Unlock()
			if shuttingDown {
				return true
			}
			c.hideOnMain()
			return false
		},
	})
	window.SetDelegate(c.windowDelegate)

	c.appDelegate = appkit.NewNSApplicationDelegate(appkit.NSApplicationDelegateConfig{
		ShouldTerminate: func(_ appkit.NSApplication) appkit.NSApplicationTerminateReply {
			c.mu.Lock()
			shuttingDown := c.shuttingDown
			c.mu.Unlock()
			if shuttingDown {
				return appkit.NSTerminateNow
			}
			c.hideOnMain()
			return appkit.NSTerminateCancel
		},
	})
	c.app.SetDelegate(c.appDelegate)

	c.control.SetVMViewWithWindow(vmView, window)
	toolbar := NewVMToolbar(window, vmView, c.vm, c.vmQueue, c.control, c.vmDirectory)
	toolbar.UpdateState(vz.VZVirtualMachineState(c.vm.State()))
	c.toolbar = toolbar

	if c.headed {
		c.openOnMain()
	} else {
		c.hideOnMain()
	}
	return nil
}

func (c *vmGUIController) captureMode() string {
	if c.headed {
		return "window"
	}
	return "private-framebuffer"
}

func (c *vmGUIController) offscreenFrame() corefoundation.CGRect {
	frame := c.lastVisibleFrame
	if frame.Size.Width <= 0 || frame.Size.Height <= 0 {
		frame = c.window.Frame()
	}
	frame.Origin = corefoundation.CGPoint{X: headlessOffscreenX, Y: headlessOffscreenY}
	return frame
}

func (c *vmGUIController) openOnMain() {
	transformToForegroundApp()
	c.app.SetActivationPolicy(appkit.NSApplicationActivationPolicyRegular)
	setAppIcon(&c.app)
	if !c.menuConfigured && c.toolbar != nil {
		setupMainMenu(c.toolbar.delegateID)
		c.menuConfigured = true
	}
	frame := c.lastVisibleFrame
	if frame.Size.Width <= 0 || frame.Size.Height <= 0 {
		frame = c.window.Frame()
	}
	c.window.SetFrameDisplay(frame, true)
	c.window.MakeFirstResponder(vmViewAsNSView(c.vmView).NSResponder)
	c.window.MakeKeyAndOrderFront(nil)
	c.window.DisplayIfNeeded()
	c.app.Activate()
	c.headed = true
	c.updateStateOnMain(vz.VZVirtualMachineState(c.vm.State()))
}

func (c *vmGUIController) hideOnMain() {
	if c.window.ID == 0 {
		return
	}
	if c.headed {
		c.lastVisibleFrame = c.window.Frame()
		c.window.SaveFrameUsingName(c.frameAutosave)
	}
	c.window.SetFrameDisplay(c.offscreenFrame(), false)
	c.window.OrderFrontRegardless()
	c.window.DisplayIfNeeded()
	c.app.SetActivationPolicy(appkit.NSApplicationActivationPolicyAccessory)
	c.app.Deactivate()
	c.headed = false
}

func (c *vmGUIController) rebuildHiddenPresentationOnMain() error {
	if c.window.ID != 0 {
		if c.headed {
			c.lastVisibleFrame = c.window.Frame()
			c.window.SaveFrameUsingName(c.frameAutosave)
		}
		c.shuttingDown = true
		c.window.OrderOut(nil)
		c.window.Close()
		c.shuttingDown = false
	}
	c.window = appkit.NSWindow{}
	c.vmView = vz.VZVirtualMachineView{}
	c.toolbar = nil
	c.windowDelegate = appkit.NSWindowDelegateObject{}
	c.appDelegate = appkit.NSApplicationDelegateObject{}
	c.headed = false
	return c.initWindow()
}

func (c *vmGUIController) updateStateOnMain(state vz.VZVirtualMachineState) {
	if c.toolbar != nil {
		c.toolbar.UpdateState(state)
	}
	if c.window.ID != 0 {
		c.window.SetTitle(fmt.Sprintf("%s — %s", c.windowTitleBase, vmStateName(state)))
	}
}

func (c *vmGUIController) shutdownOnMain() {
	c.mu.Lock()
	c.shuttingDown = true
	c.mu.Unlock()
	if c.window.ID == 0 {
		return
	}
	if c.headed {
		c.window.SaveFrameUsingName(c.frameAutosave)
	}
	c.window.OrderOut(nil)
	c.window.Close()
	c.window = appkit.NSWindow{}
	c.vmView = vz.VZVirtualMachineView{}
}

func (c *vmGUIController) Open() error {
	DispatchSync(GetMainDispatchQueue(), func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.openOnMain()
	})
	return nil
}

func (c *vmGUIController) Close() error {
	var err error
	DispatchSync(GetMainDispatchQueue(), func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		err = c.rebuildHiddenPresentationOnMain()
	})
	return err
}

func (c *vmGUIController) Status() GUIStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return GUIStatus{
		Supported:   true,
		Headed:      c.headed,
		WindowReady: c.window.ID != 0 && c.vmView.ID != 0,
		CaptureMode: c.captureMode(),
	}
}

func (c *vmGUIController) Shutdown() {
	DispatchSync(GetMainDispatchQueue(), func() {
		c.shutdownOnMain()
	})
}

func (s *ControlServer) SetGUIController(ctrl VMGUIController) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gui = ctrl
}

func (s *ControlServer) handleGUIRequest(reqType string) *controlpb.ControlResponse {
	s.mu.Lock()
	ctrl := s.gui
	windowReady := s.window.ID != 0 && s.vmView.ID != 0
	s.mu.Unlock()

	status := GUIStatus{
		Supported:   ctrl != nil,
		Headed:      windowReady,
		WindowReady: windowReady,
	}
	if ctrl != nil {
		status = ctrl.Status()
	}
	refreshStatus := func() {
		capture := s.captureBackend()
		input := s.inputBackend()
		status.AutomationBackend = combinedAutomationBackend(capture, input)
		status.CaptureBackend = capture.String()
		status.InputBackend = input.inputString()
		status.CaptureMode = capture.captureMode(status.Headed)
		status.KeyboardMode = input.keyboardMode()
		status.MouseMode = input.mouseMode()
	}
	refreshStatus()

	switch reqType {
	case "gui-open":
		if ctrl == nil {
			return &controlpb.ControlResponse{Error: "gui control unavailable for this runtime"}
		}
		if err := ctrl.Open(); err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("gui open: %v", err)}
		}
		status = ctrl.Status()
		refreshStatus()
	case "gui-close":
		if ctrl == nil {
			return &controlpb.ControlResponse{Error: "gui control unavailable for this runtime"}
		}
		if err := ctrl.Close(); err != nil {
			return &controlpb.ControlResponse{Error: fmt.Sprintf("gui close: %v", err)}
		}
		status = ctrl.Status()
		refreshStatus()
	case "gui-backend-auto":
		s.setCaptureBackend(automationBackendAuto)
		s.setInputBackend(automationBackendAuto)
		refreshStatus()
	case "gui-backend-framebuffer":
		s.setCaptureBackend(automationBackendFramebuffer)
		s.setInputBackend(automationBackendFramebuffer)
		refreshStatus()
	case "gui-backend-window":
		s.setCaptureBackend(automationBackendWindow)
		s.setInputBackend(automationBackendWindow)
		refreshStatus()
	case "gui-capture-backend-auto":
		s.setCaptureBackend(automationBackendAuto)
		refreshStatus()
	case "gui-capture-backend-framebuffer":
		s.setCaptureBackend(automationBackendFramebuffer)
		refreshStatus()
	case "gui-capture-backend-window":
		s.setCaptureBackend(automationBackendWindow)
		refreshStatus()
	case "gui-input-backend-auto":
		s.setInputBackend(automationBackendAuto)
		refreshStatus()
	case "gui-input-backend-direct":
		s.setInputBackend(automationBackendFramebuffer)
		refreshStatus()
	case "gui-input-backend-window":
		s.setInputBackend(automationBackendWindow)
		refreshStatus()
	case "gui-status":
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown gui command: %s", reqType)}
	}

	data, _ := json.Marshal(status)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_Message{
			Message: &controlpb.MessageResponse{Message: string(data)},
		},
	}
}
