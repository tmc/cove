// Menubar application for macOS VM control.
//
// Provides a system menu bar status item for controlling the virtual machine
// with options to start, stop, show/hide the display window, and monitor state.
package main

import (
	"fmt"
	"sync"

	"github.com/tmc/appledocs/generated/appkit"
	"github.com/tmc/appledocs/generated/corefoundation"
	"github.com/tmc/appledocs/generated/objc"
	"github.com/tmc/appledocs/generated/objectivec"
	vz "github.com/tmc/appledocs/generated/virtualization"
)

// MenubarApp manages a system menu bar interface for VM control.
type MenubarApp struct {
	statusItem appkit.NSStatusItem
	menu       appkit.NSMenu
	vm         vz.VZVirtualMachine
	vmView     vz.VZVirtualMachineView
	window     appkit.NSWindow
	vmDir      string

	mu            sync.Mutex
	vmState       string
	windowVisible bool
	delegate      objc.ID
}

// Menu item indices for direct access.
const (
	menuIdxStartVM    = 0
	menuIdxStopVM     = 1
	menuIdxSep1       = 2
	menuIdxShowWindow = 3
	menuIdxSep2       = 4
	menuIdxStatus     = 5
	menuIdxSep3       = 6
	menuIdxQuit       = 7
)

// NewMenubarApp creates a new menubar application.
func NewMenubarApp(vmDir string) *MenubarApp {
	return &MenubarApp{
		vmDir:   vmDir,
		vmState: "Stopped",
	}
}

// Setup initializes the menubar interface.
func (m *MenubarApp) Setup() error {
	// Get the system status bar (class method, class accessor is package-private).
	statusBarID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSStatusBar")),
		objc.Sel("systemStatusBar"),
	)
	statusBar := appkit.NSStatusBarFromID(statusBarID)

	const nsVariableStatusItemLength float64 = -1
	m.statusItem = appkit.NSStatusItemFromID(statusBar.StatusItemWithLength(nsVariableStatusItemLength).GetID())

	if button := m.statusItem.Button(); button != nil && button.GetID() != 0 {
		button.SetTitle("VM")
	}

	m.menu = appkit.NewMenuWithTitle("")

	m.addMenuItem("Start VM", "startVM:", "s")
	m.addMenuItem("Stop VM", "stopVM:", "x")
	m.addSeparator()
	m.addMenuItem("Show Display", "toggleWindow:", "d")
	m.addSeparator()
	m.addMenuItem("Status: Stopped", "", "")
	m.addSeparator()
	m.addMenuItem("Quit", "terminate:", "q")

	m.statusItem.SetMenu(&m.menu)

	m.registerActions()
	return nil
}

// addMenuItem adds a menu item with title, action, and key equivalent.
func (m *MenubarApp) addMenuItem(title, action, key string) {
	var sel objc.SEL
	if action != "" {
		sel = objc.Sel(action)
	}
	item := appkit.NewMenuItemWithTitleActionKeyEquivalent(title, sel, key)
	m.menu.AddItem(&item)
}

// addSeparator adds a separator to the menu.
func (m *MenubarApp) addSeparator() {
	// SeparatorItem is a class method; class accessor is package-private.
	sepID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSMenuItem")),
		objc.Sel("separatorItem"),
	)
	sep := appkit.NSMenuItemFromID(sepID)
	m.menu.AddItem(&sep)
}

// registerActions registers Objective-C action handlers for menu items.
func (m *MenubarApp) registerActions() {
	cls, err := objc.RegisterClass(
		"VMMenuDelegate",
		objc.GetClass("NSObject"),
		nil, nil,
		[]objc.MethodDef{
			{Cmd: objc.RegisterName("startVM:"), Fn: m.handleStartVM},
			{Cmd: objc.RegisterName("stopVM:"), Fn: m.handleStopVM},
			{Cmd: objc.RegisterName("toggleWindow:"), Fn: m.handleToggleWindow},
		},
	)
	if err != nil {
		fmt.Printf("Warning: could not register delegate class: %v\n", err)
		return
	}

	m.delegate = objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	m.delegate = objc.Send[objc.ID](m.delegate, objc.Sel("init"))

	// Set delegate as target for custom actions (Start, Stop, Show Display)
	delegateObj := objectivec.ObjectFrom(objc.ID(m.delegate))
	for _, i := range []int{menuIdxStartVM, menuIdxStopVM, menuIdxShowWindow} {
		item := m.menu.ItemAtIndex(i)
		if item != nil {
			item.SetTarget(delegateObj)
		}
	}
}

// createWindow creates the VM display window with the VZVirtualMachineView.
func (m *MenubarApp) createWindow() {
	m.vmView = vz.NewVZVirtualMachineView()
	m.vmView.SetCapturesSystemKeys(true)
	m.vmView.SetAutomaticallyReconfiguresDisplay(true)

	m.mu.Lock()
	if m.vm.ID != 0 {
		m.vmView.SetVirtualMachine(&m.vm)
	}
	m.mu.Unlock()

	m.window = appkit.NewWindowWithContentRectStyleMaskBackingDefer(
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 100, Y: 100},
			Size:   corefoundation.CGSize{Width: 1024, Height: 768},
		},
		appkit.NSWindowStyleMaskTitled|
			appkit.NSWindowStyleMaskClosable|
			appkit.NSWindowStyleMaskMiniaturizable|
			appkit.NSWindowStyleMaskResizable,
		appkit.NSBackingStoreBuffered,
		false,
	)
	m.window.SetTitle("macOS VM")
	m.window.SetContentView(&m.vmView.NSView)
	m.window.Center()

	// Prevent window from releasing on close so we can re-show it
	m.window.SetReleasedWhenClosed(false)
}

// showWindow shows the VM display window, creating it if needed.
func (m *MenubarApp) showWindow() {
	if m.window.ID == 0 {
		m.createWindow()
	}

	// Switch to Regular so the app appears in Cmd+Tab
	app := getSharedApp()
	app.SetActivationPolicy(appkit.NSApplicationActivationPolicyRegular)

	m.window.MakeKeyAndOrderFront(nil)
	app.ActivateIgnoringOtherApps(true)

	m.windowVisible = true
	m.updateWindowMenuItem()
}

// hideWindow hides the VM display window.
func (m *MenubarApp) hideWindow() {
	if m.window.ID != 0 {
		m.window.OrderOut(nil)
	}

	// Switch back to Accessory so the app leaves Cmd+Tab
	app := getSharedApp()
	app.SetActivationPolicy(appkit.NSApplicationActivationPolicyAccessory)

	m.windowVisible = false
	m.updateWindowMenuItem()
}

// updateWindowMenuItem updates the Show/Hide Display menu item text.
func (m *MenubarApp) updateWindowMenuItem() {
	item := m.menu.ItemAtIndex(menuIdxShowWindow)
	if item == nil {
		return
	}
	title := "Show Display"
	if m.windowVisible {
		title = "Hide Display"
	}
	item.SetTitle(title)
}

// handleToggleWindow is called when the Show/Hide Display menu item is clicked.
func (m *MenubarApp) handleToggleWindow(_ objc.ID, _ objc.SEL, _ objc.ID) {
	if m.windowVisible {
		m.hideWindow()
	} else {
		m.showWindow()
	}
}

// handleStartVM is called when the Start VM menu item is clicked.
func (m *MenubarApp) handleStartVM(_ objc.ID, _ objc.SEL, _ objc.ID) {
	go func() {
		m.mu.Lock()
		if m.vmState == "Running" {
			m.mu.Unlock()
			return
		}
		m.vmState = "Starting"
		m.mu.Unlock()

		m.updateStatus("Starting...")
		fmt.Println("Starting VM...")

		m.mu.Lock()
		m.vmState = "Running"
		m.mu.Unlock()

		m.updateStatus("Running")
	}()
}

// handleStopVM is called when the Stop VM menu item is clicked.
func (m *MenubarApp) handleStopVM(_ objc.ID, _ objc.SEL, _ objc.ID) {
	go func() {
		m.mu.Lock()
		if m.vmState != "Running" {
			m.mu.Unlock()
			return
		}
		m.vmState = "Stopping"
		m.mu.Unlock()

		m.updateStatus("Stopping...")
		fmt.Println("Stopping VM...")

		m.mu.Lock()
		m.vmState = "Stopped"
		m.mu.Unlock()

		m.updateStatus("Stopped")
	}()
}

// updateStatus updates the menu status display and menubar button title.
func (m *MenubarApp) updateStatus(status string) {
	DispatchAsync(getMainQueue(), func() {
		if item := m.menu.ItemAtIndex(menuIdxStatus); item != nil {
			item.SetTitle("Status: " + status)
		}

		if button := m.statusItem.Button(); button != nil && button.GetID() != 0 {
			icon := "VM"
			switch status {
			case "Running":
				icon = "VM▶"
			case "Stopped":
				icon = "VM■"
			case "Starting...", "Stopping...":
				icon = "VM⋯"
			}
			button.SetTitle(icon)
		}

		// Update window title with state
		if m.window.ID != 0 {
			m.window.SetTitle("macOS VM — " + status)
		}
	})
}

// getMainQueue returns the main dispatch queue handle.
func getMainQueue() uintptr {
	return GetMainDispatchQueue()
}

// getSharedApp returns the shared NSApplication instance.
func getSharedApp() appkit.NSApplication {
	nsAppID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSApplication")),
		objc.Sel("sharedApplication"),
	)
	return appkit.NSApplicationFromID(nsAppID)
}

// RunMenubarApp starts the menubar application.
func RunMenubarApp(vmDir string) error {
	menuApp := NewMenubarApp(vmDir)

	nsApp := getSharedApp()
	nsApp.SetActivationPolicy(appkit.NSApplicationActivationPolicyAccessory)

	if err := menuApp.Setup(); err != nil {
		return fmt.Errorf("setup menubar: %w", err)
	}

	fmt.Println("VM menubar app running. Use the VM menu in the status bar.")

	nsApp.Run()
	return nil
}
