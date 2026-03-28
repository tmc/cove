package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
)

var statusItemDelegateSerial uint64

type VMStatusItemController struct {
	app        appkit.NSApplication
	statusBar  appkit.NSStatusBar
	statusItem appkit.NSStatusItem
	menu       appkit.NSMenu
	name       string
	vm         vz.VZVirtualMachine
	vmQueue    dispatch.Queue
	control    *ControlServer
	gui        VMGUIController
	toolbar    *VMToolbar
	quit       func()

	mu         sync.Mutex
	window     appkit.NSWindow
	state      vz.VZVirtualMachineState
	delegateID objc.ID
}

func NewVMStatusItemController(app appkit.NSApplication, vm vz.VZVirtualMachine, queue dispatch.Queue, control *ControlServer, window appkit.NSWindow, gui VMGUIController, toolbar *VMToolbar, quit func()) *VMStatusItemController {
	c := &VMStatusItemController{
		app:     app,
		name:    statusItemVMName(),
		vm:      vm,
		vmQueue: queue,
		control: control,
		window:  window,
		gui:     gui,
		toolbar: toolbar,
		quit:    quit,
		state:   vz.VZVirtualMachineState(vm.State()),
	}
	c.initOnMain()
	return c
}

func (c *VMStatusItemController) initOnMain() {
	c.registerDelegate()
	c.statusBar = appkit.GetNSStatusBarClass().SystemStatusBar()
	item := c.statusBar.StatusItemWithLength(appkit.VariableStatusItemLength)
	c.statusItem = appkit.NSStatusItemFromID(item.GetID())
	c.statusItem.SetAutosaveName(appkit.NSStatusItemAutosaveName(c.autosaveName()))
	c.statusItem.SetVisible(true)

	menu := appkit.NewMenuWithTitle(c.menuTitle())
	menu.SetDelegate(appkit.NSMenuDelegateObjectFromID(c.delegateID))
	c.menu = menu
	c.statusItem.SetMenu(&menu)

	c.refreshStatusItem()
}

func (c *VMStatusItemController) registerDelegate() {
	className := fmt.Sprintf("VMStatusItemDelegate_%d", atomic.AddUint64(&statusItemDelegateSerial, 1))
	cls, err := objc.RegisterClass(
		className,
		objc.GetClass("NSObject"),
		nil, nil,
		[]objc.MethodDef{
			{Cmd: objc.RegisterName("menuNeedsUpdate:"), Fn: c.handleMenuNeedsUpdate},
			{Cmd: objc.RegisterName("toggleWindow:"), Fn: c.handleToggleWindow},
			{Cmd: objc.RegisterName("toggleRunState:"), Fn: c.handleToggleRunState},
			{Cmd: objc.RegisterName("stopVM:"), Fn: c.handleStop},
			{Cmd: objc.RegisterName("restartVM:"), Fn: c.handleRestart},
			{Cmd: objc.RegisterName("bootRecovery:"), Fn: c.handleBootRecovery},
			{Cmd: objc.RegisterName("suspendVM:"), Fn: c.handleSuspend},
			{Cmd: objc.RegisterName("takeScreenshot:"), Fn: c.handleScreenshot},
			{Cmd: objc.RegisterName("quitVMRuntime:"), Fn: c.handleQuit},
		},
	)
	if err != nil {
		fmt.Printf("warning: could not register status item delegate: %v\n", err)
		return
	}
	c.delegateID = objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	c.delegateID = objc.Send[objc.ID](c.delegateID, objc.Sel("init"))
}

func (c *VMStatusItemController) autosaveName() string {
	return "com.vz-macos.status." + c.name
}

func (c *VMStatusItemController) buttonTitle() string {
	const maxTitleRunes = 18
	prefix := c.buttonStatePrefix(c.currentState())
	nameWidth := maxTitleRunes - len([]rune(prefix))
	if nameWidth < 6 {
		nameWidth = 6
	}
	return prefix + truncateMiddle(c.name, nameWidth)
}

func (c *VMStatusItemController) tooltipForState(state vz.VZVirtualMachineState) string {
	return fmt.Sprintf("vz-macos %s — %s (%s)", c.name, vmStateName(state), c.presentationMode())
}

func (c *VMStatusItemController) setWindow(window appkit.NSWindow) {
	c.mu.Lock()
	c.window = window
	c.mu.Unlock()
	c.refreshStatusItem()
}

func (c *VMStatusItemController) UpdateState(state vz.VZVirtualMachineState) {
	c.mu.Lock()
	c.state = state
	c.mu.Unlock()
	c.refreshStatusItem()
}

func (c *VMStatusItemController) currentState() vz.VZVirtualMachineState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *VMStatusItemController) refreshStatusItem() {
	if c.statusItem.ID == 0 {
		return
	}
	state := c.currentState()
	button := c.statusItem.Button()
	button.SetTitle(c.buttonTitle())
	button.SetToolTip(c.tooltipForState(state))
	c.menu.SetTitle(c.menuTitle())
}

func (c *VMStatusItemController) Shutdown() {
	if c.statusItem.ID == 0 || c.statusBar.ID == 0 {
		return
	}
	c.statusBar.RemoveStatusItem(c.statusItem)
	c.statusItem = appkit.NSStatusItem{}
	c.menu = appkit.NSMenu{}
}

func (c *VMStatusItemController) handleMenuNeedsUpdate(_ objc.ID, _ objc.SEL, menuID objc.ID) {
	menu := appkit.NSMenuFromID(menuID)
	objc.Send[struct{}](menu.ID, objc.Sel("removeAllItems"))

	c.mu.Lock()
	state := c.state
	window := c.window
	toolbar := c.toolbar
	c.mu.Unlock()

	menu.SetTitle(c.menuTitle())
	header := appkit.NewMenuItemWithTitleActionKeyEquivalent("VM: "+c.name, 0, "")
	header.SetEnabled(false)
	menu.AddItem(&header)

	stateItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("State: "+vmStateName(state)+" ("+c.presentationModeWithWindow(window)+")", 0, "")
	stateItem.SetEnabled(false)
	menu.AddItem(&stateItem)

	addToolbarMenuSeparator(menu)

	showTitle := "Show Window"
	if c.isWindowVisible(window) {
		showTitle = "Hide Window"
	}
	addToolbarMenuItem(menu, showTitle, "toggleWindow:", c.delegateID)

	runItem := appkit.NewMenuItemWithTitleActionKeyEquivalent(c.runStateTitle(state), objc.Sel("toggleRunState:"), "")
	runItem.SetTarget(objectivec.ObjectFromID(c.delegateID))
	runItem.SetEnabled(c.runStateEnabled(state))
	menu.AddItem(&runItem)

	stopItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Stop", objc.Sel("stopVM:"), "")
	stopItem.SetTarget(objectivec.ObjectFromID(c.delegateID))
	stopItem.SetEnabled(state == vz.VZVirtualMachineStateRunning || state == vz.VZVirtualMachineStatePaused)
	menu.AddItem(&stopItem)

	if !linuxMode {
		recoveryItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Boot to Recovery", objc.Sel("bootRecovery:"), "")
		recoveryItem.SetTarget(objectivec.ObjectFromID(c.delegateID))
		recoveryItem.SetEnabled(state == vz.VZVirtualMachineStateRunning || state == vz.VZVirtualMachineStatePaused || state == vz.VZVirtualMachineStateStopped)
		menu.AddItem(&recoveryItem)
	}

	addToolbarMenuSeparator(menu)

	screenshotItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Screenshot...", objc.Sel("takeScreenshot:"), "")
	screenshotItem.SetTarget(objectivec.ObjectFromID(c.delegateID))
	screenshotItem.SetEnabled(state == vz.VZVirtualMachineStateRunning)
	menu.AddItem(&screenshotItem)

	if toolbar != nil {
		sharedMenu := appkit.NewMenuWithTitle("Shared Folders")
		toolbar.populateSharedFolderMenu(sharedMenu)
		sharedItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Shared Folders", 0, "")
		sharedItem.SetSubmenu(&sharedMenu)
		sharedItem.SetEnabled(!c.stateBusy(state))
		menu.AddItem(&sharedItem)
	}

	addToolbarMenuSeparator(menu)

	suspendItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Suspend", objc.Sel("suspendVM:"), "")
	suspendItem.SetTarget(objectivec.ObjectFromID(c.delegateID))
	suspendItem.SetEnabled(canSaveRestore && !c.stateBusy(state) && state != vz.VZVirtualMachineStateStopped)
	menu.AddItem(&suspendItem)

	restartItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Restart", objc.Sel("restartVM:"), "")
	restartItem.SetTarget(objectivec.ObjectFromID(c.delegateID))
	restartItem.SetEnabled(state == vz.VZVirtualMachineStateRunning)
	menu.AddItem(&restartItem)

	addToolbarMenuSeparator(menu)
	addToolbarMenuItem(menu, "Quit", "quitVMRuntime:", c.delegateID)
}

func (c *VMStatusItemController) stateBusy(state vz.VZVirtualMachineState) bool {
	return state == vz.VZVirtualMachineStateStarting ||
		state == vz.VZVirtualMachineStateStopping ||
		state == vz.VZVirtualMachineStateSaving ||
		state == vz.VZVirtualMachineStateRestoring
}

func (c *VMStatusItemController) runStateTitle(state vz.VZVirtualMachineState) string {
	switch state {
	case vz.VZVirtualMachineStateRunning:
		return "Pause"
	case vz.VZVirtualMachineStatePaused:
		return "Resume"
	case vz.VZVirtualMachineStateStopped:
		return "Start"
	default:
		return vmStateName(state)
	}
}

func (c *VMStatusItemController) runStateEnabled(state vz.VZVirtualMachineState) bool {
	return !c.stateBusy(state) &&
		(state == vz.VZVirtualMachineStateRunning ||
			state == vz.VZVirtualMachineStatePaused ||
			state == vz.VZVirtualMachineStateStopped)
}

func (c *VMStatusItemController) isWindowVisible(window appkit.NSWindow) bool {
	if c.gui != nil {
		return c.gui.Status().Headed
	}
	return window.ID != 0 && window.Visible() && !window.Miniaturized()
}

func (c *VMStatusItemController) presentationMode() string {
	c.mu.Lock()
	window := c.window
	c.mu.Unlock()
	return c.presentationModeWithWindow(window)
}

func (c *VMStatusItemController) presentationModeWithWindow(window appkit.NSWindow) string {
	if c.gui != nil {
		if c.gui.Status().Headed {
			return "window"
		}
		return "headless"
	}
	if window.ID != 0 && window.Visible() && !window.Miniaturized() {
		return "window"
	}
	if window.ID != 0 {
		return "window hidden"
	}
	return "headless"
}

func (c *VMStatusItemController) menuTitle() string {
	return "vz-macos: " + c.name
}

func (c *VMStatusItemController) buttonStatePrefix(state vz.VZVirtualMachineState) string {
	switch state {
	case vz.VZVirtualMachineStateRunning:
		return "Rn:"
	case vz.VZVirtualMachineStatePaused:
		return "Pa:"
	case vz.VZVirtualMachineStateStopped:
		return "St:"
	case vz.VZVirtualMachineStateStarting:
		return "Up:"
	case vz.VZVirtualMachineStateStopping:
		return "Dn:"
	case vz.VZVirtualMachineStateSaving:
		return "Sv:"
	case vz.VZVirtualMachineStateRestoring:
		return "Rs:"
	default:
		return "VM:"
	}
}

func statusItemVMName() string {
	if vmName != "" {
		return vmName
	}
	if vmDir != "" {
		name := filepath.Base(vmDir)
		if name != "" && name != "." && name != string(filepath.Separator) {
			return name
		}
	}
	return "default"
}

func truncateMiddle(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	head := (maxRunes - 3) / 2
	tail := maxRunes - 3 - head
	return string(runes[:head]) + "..." + string(runes[len(runes)-tail:])
}

func (c *VMStatusItemController) scheduleAction(fn func()) {
	foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(0, false, func(_ *foundation.NSTimer) {
		fn()
	})
}

func (c *VMStatusItemController) handleToggleWindow(_ objc.ID, _ objc.SEL, _ objc.ID) {
	c.scheduleAction(c.toggleWindow)
}

func (c *VMStatusItemController) toggleWindow() {
	if c.gui != nil {
		status := c.gui.Status()
		go func() {
			var err error
			if status.Headed {
				err = c.gui.Close()
			} else {
				err = c.gui.Open()
			}
			if err != nil {
				action := "show"
				if status.Headed {
					action = "hide"
				}
				fmt.Fprintf(os.Stderr, "error: %s window: %v\n", action, err)
				return
			}
			c.scheduleAction(c.refreshStatusItem)
		}()
		return
	}

	c.mu.Lock()
	window := c.window
	c.mu.Unlock()
	if window.ID == 0 {
		return
	}
	if window.Visible() && !window.Miniaturized() {
		window.OrderOut(nil)
		c.app.Deactivate()
		c.refreshStatusItem()
		return
	}
	window.MakeKeyAndOrderFront(nil)
	c.app.Activate()
	c.refreshStatusItem()
}

func (c *VMStatusItemController) handleToggleRunState(_ objc.ID, _ objc.SEL, _ objc.ID) {
	c.scheduleAction(func() {
		toggleVMStartPause("Status item", c.vm, c.vmQueue)
	})
}

func (c *VMStatusItemController) handleStop(_ objc.ID, _ objc.SEL, _ objc.ID) {
	c.scheduleAction(func() {
		requestVMStop("Status item", c.vm, c.vmQueue)
	})
}

func (c *VMStatusItemController) handleRestart(_ objc.ID, _ objc.SEL, _ objc.ID) {
	c.scheduleAction(func() {
		restartVM("Status item", c.vm, c.vmQueue)
	})
}

func (c *VMStatusItemController) handleBootRecovery(_ objc.ID, _ objc.SEL, _ objc.ID) {
	c.scheduleAction(func() {
		bootVMToRecovery("Status item", c.vm, c.vmQueue)
	})
}

func (c *VMStatusItemController) handleSuspend(_ objc.ID, _ objc.SEL, _ objc.ID) {
	c.scheduleAction(func() {
		requestVMSuspend("Status item", c.vm, c.vmQueue)
	})
}

func (c *VMStatusItemController) handleScreenshot(_ objc.ID, _ objc.SEL, _ objc.ID) {
	c.scheduleAction(func() {
		saveCurrentVMScreenshot("Status item", c.control)
	})
}

func (c *VMStatusItemController) handleQuit(_ objc.ID, _ objc.SEL, _ objc.ID) {
	c.scheduleAction(func() {
		if c.quit != nil {
			c.quit()
			return
		}
		c.app.Terminate(nil)
	})
}
