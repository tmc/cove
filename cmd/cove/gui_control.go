package main

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/cove/internal/vmrun"

	controlpb "github.com/tmc/cove/proto/controlpb"
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

type vmGUIWindowProvider interface {
	Window() appkit.NSWindow
	Toolbar() *VMToolbar
}

type vmGUIController struct {
	app             appkit.NSApplication
	vm              vz.VZVirtualMachine
	vmQueue         dispatch.Queue
	bindings        vmGUIBindings
	target          vmSelection
	vmDirectory     string
	rc              vmrun.RunConfig
	hc              vmrun.HostConfig
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

var setDockBadgeLabel = func(app appkit.NSApplication, label string) {
	dockTile := app.DockTile()
	dockTile.SetBadgeLabel(label)
}

func ensureAppReady(policy appkit.NSApplicationActivationPolicy) appkit.NSApplication {
	var app appkit.NSApplication
	runOnUIThreadSync(func() {
		app = getSharedApp()
		app.SetActivationPolicy(policy)
		if policy == appkit.NSApplicationActivationPolicyRegular {
			setAppIcon(&app)
		}
		ensureAppLaunched(app)
	})
	return app
}

func ensureAppLaunched(app appkit.NSApplication) {
	if appFinishedLaunching {
		return
	}

	launch := func() {
		if appFinishedLaunching {
			return
		}
		app.FinishLaunching()
		appFinishedLaunching = true
	}

	runOnUIThreadSync(launch)
}

// runAppEventLoopUntil mirrors NSApplication.Run's nextEvent/sendEvent loop
// while letting CLI lifecycles stop without entering a long-lived app.Run.
func runAppEventLoopUntil(app appkit.NSApplication, stop func() bool) {
	registerUIThread()
	for !stop() {
		drainUIThreadTasks()
		objc.AutoreleasePool(func() {
			limit := foundation.GetNSDateClass().DateWithTimeIntervalSinceNow(0.05)
			event := app.NextEventMatchingMaskUntilDateInModeDequeue(nsEventMaskAny, limit, foundation.RunLoopDefaultMode, true)
			if event.GetID() == 0 {
				return
			}
			app.SendEvent(event)
			app.UpdateWindows()
		})
		drainUIThreadTasks()
	}
}

func newHeadlessGUIController(app appkit.NSApplication, target vmSelection, vm vz.VZVirtualMachine, queue dispatch.Queue, bindings vmGUIBindings, initiallyHeaded bool, rc vmrun.RunConfig, hc vmrun.HostConfig) (*vmGUIController, error) {
	c := &vmGUIController{
		app:         app,
		vm:          vm,
		vmQueue:     queue,
		bindings:    bindings,
		target:      target,
		vmDirectory: target.Directory,
		rc:          rc,
		hc:          hc,
		headed:      initiallyHeaded,
	}
	c.windowTitleBase = controllerWindowTitle(target)
	if err := c.initDetachedView(); err != nil {
		return nil, err
	}
	return c, nil
}

func newAttachedGUIController(app appkit.NSApplication, target vmSelection, vm vz.VZVirtualMachine, queue dispatch.Queue, bindings vmGUIBindings, window appkit.NSWindow, vmView vz.VZVirtualMachineView, toolbar *VMToolbar, frameAutosaveName appkit.NSWindowFrameAutosaveName, rc vmrun.RunConfig, hc vmrun.HostConfig) *vmGUIController {
	c := &vmGUIController{
		app:             app,
		vm:              vm,
		vmQueue:         queue,
		bindings:        bindings,
		target:          target,
		vmDirectory:     target.Directory,
		rc:              rc,
		hc:              hc,
		window:          window,
		vmView:          vmView,
		toolbar:         toolbar,
		frameAutosave:   frameAutosaveName,
		headed:          window.ID != 0 && vmView.ID != 0,
		windowTitleBase: controllerWindowTitle(target),
	}
	if window.ID != 0 {
		c.lastVisibleFrame = window.Frame()
	}
	if bindings != nil && vmView.ID != 0 {
		bindings.SetVMViewWithWindow(vmView, window)
	}
	return c
}

func controllerWindowTitle(target vmSelection) string {
	osLabel := "macOS VM"
	if linuxMode {
		osLabel = "Linux VM"
	}
	if target.Name == "" || target.Name == "default" {
		return osLabel
	}
	return fmt.Sprintf("%s — %s", osLabel, target.Name)
}

func (c *vmGUIController) newVMView() vz.VZVirtualMachineView {
	vmView := vz.NewVZVirtualMachineView()
	vmView.SetVirtualMachine(&c.vm)
	vmView.SetCapturesSystemKeys(false)
	vmView.SetAutomaticallyReconfiguresDisplay(true)
	vmViewAsNSView(vmView).SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{},
		Size:   corefoundation.CGSize{Width: defaultWindowWidth, Height: defaultWindowHeight},
	})
	return vmView
}

func (c *vmGUIController) configureProcessIdentity() {
	procName := "cove"
	if c.target.Name != "" && c.target.Name != "default" {
		procName = fmt.Sprintf("cove (%s)", c.target.Name)
	}
	foundation.GetProcessInfoClass().ProcessInfo().SetProcessName(procName)
}

func (c *vmGUIController) setDockBadgeOnMain() {
	if c.target.Name != "" && c.target.Name != "default" {
		setDockBadgeLabel(c.app, c.target.Name)
	}
}

func (c *vmGUIController) initDetachedView() error {
	if c.vmView.ID == 0 {
		c.vmView = c.newVMView()
	}
	if c.window.ID == 0 {
		if err := c.initWindow(); err != nil {
			return err
		}
	}
	c.window.OrderOut(nil)
	c.headed = false
	if c.bindings != nil {
		c.bindings.SetVMViewWithWindow(c.vmView, c.window)
	}
	return nil
}

func (c *vmGUIController) initWindow() error {
	if c.vmView.ID == 0 {
		c.vmView = c.newVMView()
	}

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
	window.SetContentView(vmViewAsNSView(c.vmView))
	if !restoredFrame {
		window.Center()
	}
	c.window = window
	c.frameAutosave = frameAutosaveName
	c.lastVisibleFrame = window.Frame()
	c.configureProcessIdentity()

	c.windowDelegate = appkit.NewNSWindowDelegate(appkit.NSWindowDelegateConfig{
		ShouldClose: func(_ appkit.NSWindow) bool {
			return c.windowShouldCloseOnMain()
		},
	})
	window.SetDelegate(c.windowDelegate)

	c.appDelegate = appkit.NewNSApplicationDelegate(appkit.NSApplicationDelegateConfig{
		ShouldTerminate: func(_ appkit.NSApplication) appkit.NSApplicationTerminateReply {
			if c.appShouldTerminateOnMain() {
				return appkit.NSTerminateNow
			}
			return appkit.NSTerminateCancel
		},
	})
	c.app.SetDelegate(c.appDelegate)

	if c.bindings != nil {
		c.bindings.SetVMViewWithWindow(c.vmView, window)
		toolbar := NewVMToolbar(window, c.vmView, c.vm, c.vmQueue, c.bindings, c.vmDirectory, c.rc, c.hc)
		toolbar.UpdateState(vz.VZVirtualMachineState(c.vm.State()))
		c.toolbar = toolbar
	}

	return nil
}

func (c *vmGUIController) windowShouldCloseOnMain() bool {
	return c.windowShouldCloseOnMainWithHide(c.hideOnMain)
}

func (c *vmGUIController) windowShouldCloseOnMainWithHide(hide func() error) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shuttingDown {
		return true
	}
	_ = hide()
	return false
}

func (c *vmGUIController) appShouldTerminateOnMain() bool {
	return c.appShouldTerminateOnMainWithHide(c.hideOnMain)
}

func (c *vmGUIController) appShouldTerminateOnMainWithHide(hide func() error) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shuttingDown {
		return true
	}
	_ = hide()
	return false
}

func (c *vmGUIController) captureMode() string {
	if c.headed {
		return "window"
	}
	return "private-framebuffer"
}

func (c *vmGUIController) openOnMain() {
	transformToForegroundApp()
	c.app.SetActivationPolicy(appkit.NSApplicationActivationPolicyRegular)
	setAppIcon(&c.app)
	c.setDockBadgeOnMain()
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

func (c *vmGUIController) hideOnMain() error {
	if c.window.ID != 0 {
		if c.headed {
			c.lastVisibleFrame = c.window.Frame()
			c.window.SaveFrameUsingName(c.frameAutosave)
		}
		c.window.OrderOut(nil)
	}
	c.app.SetActivationPolicy(appkit.NSApplicationActivationPolicyAccessory)
	c.app.Deactivate()
	c.headed = false
	if c.bindings != nil {
		c.bindings.SetVMViewWithWindow(c.vmView, c.window)
	}
	return nil
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
	var err error
	runOnUIThreadSync(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.window.ID == 0 {
			err = c.initWindow()
			if err != nil {
				return
			}
		}
		c.openOnMain()
	})
	return err
}

func (c *vmGUIController) Close() error {
	var err error
	runOnUIThreadSync(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		err = c.hideOnMain()
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
	runOnUIThreadSync(func() {
		c.shutdownOnMain()
	})
}

func (c *vmGUIController) Window() appkit.NSWindow {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.window
}

func (c *vmGUIController) Toolbar() *VMToolbar {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.toolbar
}

func (c *vmGUIController) setControlBindings(bindings vmGUIBindings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bindings = bindings
	if c.bindings == nil || c.vmView.ID == 0 {
		return
	}
	c.bindings.SetVMViewWithWindow(c.vmView, c.window)
	if c.window.ID != 0 && c.toolbar == nil {
		toolbar := NewVMToolbar(c.window, c.vmView, c.vm, c.vmQueue, c.bindings, c.vmDirectory, c.rc, c.hc)
		toolbar.UpdateState(vz.VZVirtualMachineState(c.vm.State()))
		c.toolbar = toolbar
	}
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
