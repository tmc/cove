package main

import (
	"fmt"
	"unsafe"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/vz-macos/internal/assets"
)

// VMStatusItem manages a system menu bar status item that mirrors VM state.
// It is created alongside the VM window and toolbar during runVMWithGUI.
type VMStatusItem struct {
	statusItem appkit.NSStatusItem
	menu       appkit.NSMenu
	statusIdx  int // index of the status menu item
	window     appkit.NSWindow
	toolbar    *VMToolbar
	vmName     string
}

// NewVMStatusItem creates a system status bar item for the running VM.
// The toolbar delegate is reused for action targets (stop, pause, etc.).
func NewVMStatusItem(window appkit.NSWindow, toolbar *VMToolbar, vmName string) *VMStatusItem {
	s := &VMStatusItem{
		window:  window,
		toolbar: toolbar,
		vmName:  vmName,
	}
	s.setup()
	return s
}

func (s *VMStatusItem) setup() {
	statusBarID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSStatusBar")),
		objc.Sel("systemStatusBar"),
	)
	statusBar := appkit.NSStatusBarFromID(statusBarID)

	const nsVariableStatusItemLength float64 = -1
	s.statusItem = appkit.NSStatusItemFromID(
		statusBar.StatusItemWithLength(nsVariableStatusItemLength).GetID(),
	)

	if button := s.statusItem.Button(); button != nil && button.GetID() != 0 {
		iconData := assets.Icon
		nsData := foundation.NewDataWithBytesLength(unsafe.Pointer(&iconData[0]), uint(len(iconData)))
		img := appkit.NewImageWithData(&nsData)
		if img.ID != 0 {
			img.SetSize(corefoundation.CGSize{Width: 18, Height: 18})
			img.SetTemplate(true) // adapts to light/dark menu bar
			objc.Send[objc.ID](button.GetID(), objc.Sel("setImage:"), img.ID)
		} else {
			button.SetTitle("VZ")
		}
	}

	s.menu = appkit.NewMenuWithTitle("")
	target := s.toolbar.delegateID

	s.addActionItem("Pause / Resume", "startPauseVM:", "p", target) // 0
	s.addActionItem("Stop", "stopVM:", "", target)                  // 1
	s.addActionItem("Restart", "restartVM:", "", target)            // 2
	s.addSeparator()                                                // 3

	// Shared Folders submenu — dynamically rebuilt via menuNeedsUpdate:
	sharedMenu := appkit.NewMenuWithTitle("Shared Folders")
	delegateObj := appkit.NSMenuDelegateObjectFromID(target)
	sharedMenu.SetDelegate(delegateObj)
	addMainMenuItem(sharedMenu, "Add Folder...", "addSharedFolder:", "", target)
	sharedMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Shared Folders", 0, "")
	sharedMenuItem.SetSubmenu(&sharedMenu)
	s.menu.AddItem(&sharedMenuItem) // 4

	s.addActionItem("Show Window", "showVMWindow:", "", target) // 5
	s.addSeparator()                                            // 6
	s.addItem(fmt.Sprintf("%s \u2014 Starting...", s.vmName))   // 7
	s.statusIdx = 7
	s.addSeparator()                              // 8
	s.addActionItem("Quit", "terminate:", "q", 0) // 9

	s.statusItem.SetMenu(&s.menu)
}

func (s *VMStatusItem) addActionItem(title, action, key string, target objc.ID) {
	var sel objc.SEL
	if action != "" {
		sel = objc.Sel(action)
	}
	item := appkit.NewMenuItemWithTitleActionKeyEquivalent(title, sel, key)
	if target != 0 {
		item.SetTarget(objectivec.ObjectFromID(target))
	}
	s.menu.AddItem(&item)
}

func (s *VMStatusItem) addItem(title string) {
	item := appkit.NewMenuItemWithTitleActionKeyEquivalent(title, 0, "")
	item.SetEnabled(false)
	s.menu.AddItem(&item)
}

func (s *VMStatusItem) addSeparator() {
	sepID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSMenuItem")),
		objc.Sel("separatorItem"),
	)
	sep := appkit.NSMenuItemFromID(sepID)
	s.menu.AddItem(&sep)
}

// UpdateState updates the status item icon and status text for the current VM state.
func (s *VMStatusItem) UpdateState(state vz.VZVirtualMachineState) {
	name := vmStateName(state)

	// Update status text
	if item := s.menu.ItemAtIndex(s.statusIdx); item != nil {
		item.SetTitle(fmt.Sprintf("%s \u2014 %s", s.vmName, name))
	}

	// Enable/disable actions based on state
	running := state == vz.VZVirtualMachineStateRunning
	paused := state == vz.VZVirtualMachineStatePaused

	// Pause/Resume (index 0)
	if item := s.menu.ItemAtIndex(0); item != nil {
		item.SetEnabled(running || paused)
		if paused {
			item.SetTitle("Resume")
		} else {
			item.SetTitle("Pause")
		}
	}
	// Stop (index 1)
	if item := s.menu.ItemAtIndex(1); item != nil {
		item.SetEnabled(running || paused)
	}
	// Restart (index 2)
	if item := s.menu.ItemAtIndex(2); item != nil {
		item.SetEnabled(running)
	}
}
