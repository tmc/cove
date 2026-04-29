package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
)

// Toolbar item identifiers.
const (
	toolbarIDStop         = "stopVM"
	toolbarIDStartPause   = "startPause"
	toolbarIDRestart      = "restart"
	toolbarIDBootOptions  = "bootOptions"
	toolbarIDCaptureInput = "captureInput"
	toolbarIDScreenshot   = "screenshot"
	toolbarIDSharedFolder = "sharedFolder"
)

// sharedFolderMenuTag is the base tag for shared folder menu items.
// Each folder's index is added to this base to identify it for removal.
const sharedFolderMenuTag = 1000

// SharedFoldersVirtioFSTag is the VirtioFS tag for the dedicated shared folders device.
// This device is always created at boot so the toolbar can hotplug folders at runtime.
const SharedFoldersVirtioFSTag = "_shared-folders"

// modalResponseOKCode is NSModalResponseOK from AppKit.
// Use the numeric value to stay compatible across apple binding revisions.
const modalResponseOKCode = 1000

var toolbarDelegateSerial uint64

func isModalResponseOK(response appkit.NSModalResponse) bool {
	return int64(response) == modalResponseOKCode
}

// VMToolbar manages the NSToolbar for a VM window.
type VMToolbar struct {
	toolbar        appkit.NSToolbar
	window         appkit.NSWindow
	vmView         vz.VZVirtualMachineView
	vm             vz.VZVirtualMachine
	vmQueue        dispatch.Queue
	vmDirectory    string
	delegateID     objc.ID
	items          map[string]appkit.NSToolbarItem
	screenshots    vmScreenshotProvider
	captureEnabled bool
	installing     bool // true during installation (disables most VM controls)
}

// NewVMToolbar creates and attaches a toolbar to the VM window.
func NewVMToolbar(window appkit.NSWindow, vmView vz.VZVirtualMachineView, vm vz.VZVirtualMachine, queue dispatch.Queue, screenshots vmScreenshotProvider, vmDirectory string) *VMToolbar {
	t := &VMToolbar{
		window:      window,
		vmView:      vmView,
		vm:          vm,
		vmQueue:     queue,
		vmDirectory: vmDirectory,
		items:       make(map[string]appkit.NSToolbarItem),
		screenshots: screenshots,
	}

	t.registerDelegate()

	t.toolbar = appkit.NewToolbarWithIdentifier("com.cove.vmToolbar")
	t.toolbar.SetDisplayMode(nsToolbarDisplayModeIconOnly)

	delegateObj := appkit.NSToolbarDelegateObjectFromID(t.delegateID)
	t.toolbar.SetDelegate(delegateObj)

	window.SetToolbar(&t.toolbar)
	window.SetToolbarStyle(appkit.NSWindowToolbarStyleUnified)

	return t
}

// registerDelegate registers the Objective-C delegate class for toolbar callbacks.
func (t *VMToolbar) registerDelegate() {
	className := fmt.Sprintf("VMToolbarDelegate_%d", atomic.AddUint64(&toolbarDelegateSerial, 1))
	cls, err := objc.RegisterClass(
		className,
		objc.GetClass("NSObject"),
		nil, nil,
		[]objc.MethodDef{
			{Cmd: objc.RegisterName("toolbar:itemForItemIdentifier:willBeInsertedIntoToolbar:"), Fn: t.itemForIdentifier},
			{Cmd: objc.RegisterName("toolbarDefaultItemIdentifiers:"), Fn: t.defaultIdentifiers},
			{Cmd: objc.RegisterName("toolbarAllowedItemIdentifiers:"), Fn: t.allowedIdentifiers},
			{Cmd: objc.RegisterName("stopVM:"), Fn: t.handleStop},
			{Cmd: objc.RegisterName("startPauseVM:"), Fn: t.handleStartPause},
			{Cmd: objc.RegisterName("restartVM:"), Fn: t.handleRestart},
			{Cmd: objc.RegisterName("bootRecovery:"), Fn: t.handleBootRecovery},
			{Cmd: objc.RegisterName("suspendVM:"), Fn: t.handleSuspend},
			{Cmd: objc.RegisterName("captureInput:"), Fn: t.handleCaptureInput},
			{Cmd: objc.RegisterName("takeScreenshot:"), Fn: t.handleScreenshot},
			{Cmd: objc.RegisterName("addSharedFolder:"), Fn: t.handleAddSharedFolder},
			{Cmd: objc.RegisterName("removeSharedFolder:"), Fn: t.handleRemoveSharedFolder},
			{Cmd: objc.RegisterName("removeAllSharedFolders:"), Fn: t.handleRemoveAllSharedFolders},
			{Cmd: objc.RegisterName("menuNeedsUpdate:"), Fn: t.handleMenuNeedsUpdate},
			{Cmd: objc.RegisterName("showVMWindow:"), Fn: t.handleShowWindow},
		},
	)
	if err != nil {
		fmt.Printf("warning: could not register toolbar delegate: %v\n", err)
		return
	}

	t.delegateID = objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	t.delegateID = objc.Send[objc.ID](t.delegateID, objc.Sel("init"))
}

// toolbarItemIDs returns the ordered list of toolbar item identifiers.
func toolbarItemIDs() []string {
	return []string{
		toolbarIDStop,
		toolbarIDStartPause,
		toolbarIDRestart,
		toolbarIDBootOptions,
		string(appkit.ToolbarFlexibleSpaceItemIdentifier),
		toolbarIDCaptureInput,
		toolbarIDScreenshot,
		toolbarIDSharedFolder,
	}
}

// defaultIdentifiers is the NSToolbarDelegate method returning default item identifiers.
func (t *VMToolbar) defaultIdentifiers(_ objc.ID, _ objc.SEL, _ objc.ID) objc.ID {
	return objectivec.StringSliceToNSArray(toolbarItemIDs())
}

// allowedIdentifiers is the NSToolbarDelegate method returning allowed item identifiers.
func (t *VMToolbar) allowedIdentifiers(_ objc.ID, _ objc.SEL, _ objc.ID) objc.ID {
	return objectivec.StringSliceToNSArray(toolbarItemIDs())
}

// itemForIdentifier is the NSToolbarDelegate method that creates toolbar items on demand.
func (t *VMToolbar) itemForIdentifier(_ objc.ID, _ objc.SEL, _ objc.ID, identifierID objc.ID, _ bool) objc.ID {
	identifier := foundation.NSStringFromID(identifierID).String()

	if item, ok := t.items[identifier]; ok {
		return item.ID
	}

	var item appkit.NSToolbarItem
	switch identifier {
	case toolbarIDStop:
		item = t.createItem(toolbarIDStop, "stop.fill", "Stop", "stopVM:")
	case toolbarIDStartPause:
		item = t.createItem(toolbarIDStartPause, "pause.fill", "Pause", "startPauseVM:")
	case toolbarIDRestart:
		item = t.createItem(toolbarIDRestart, "arrow.counterclockwise", "Restart", "restartVM:")
	case toolbarIDBootOptions:
		menuItem := t.createMenuToolbarItem(toolbarIDBootOptions, "wrench.and.screwdriver", "Boot Options")
		objc.Send[objc.ID](menuItem.ID, objc.Sel("retain"))
		t.items[identifier] = menuItem.NSToolbarItem
		return menuItem.ID
	case toolbarIDCaptureInput:
		item = t.createItem(toolbarIDCaptureInput, "keyboard", "Capture Input", "captureInput:")
	case toolbarIDScreenshot:
		item = t.createItem(toolbarIDScreenshot, "camera", "Screenshot", "takeScreenshot:")
	case toolbarIDSharedFolder:
		menuItem := t.createSharedFolderMenu()
		objc.Send[objc.ID](menuItem.ID, objc.Sel("retain"))
		t.items[identifier] = menuItem.NSToolbarItem
		return menuItem.ID
	default:
		return 0
	}

	objc.Send[objc.ID](item.ID, objc.Sel("retain"))
	t.items[identifier] = item
	return item.ID
}

// createItem creates a single toolbar item with an SF Symbol image.
func (t *VMToolbar) createItem(id, sfSymbol, label, action string) appkit.NSToolbarItem {
	item := appkit.NewToolbarItemWithItemIdentifier(appkit.NSToolbarItemIdentifier(id))

	img := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(sfSymbol, label)
	item.SetImage(&img)

	item.SetLabel(label)
	item.SetPaletteLabel(label)
	item.SetToolTip(label)
	item.SetBordered(true)
	item.SetAutovalidates(false)

	item.SetTarget(objectivec.ObjectFromID(t.delegateID))
	item.SetAction(objc.Sel(action))

	return item
}

// createMenuToolbarItem creates a dropdown menu toolbar item with an SF Symbol icon.
func (t *VMToolbar) createMenuToolbarItem(id, sfSymbol, label string) appkit.NSMenuToolbarItem {
	menuItem := appkit.NewMenuToolbarItemWithItemIdentifier(appkit.NSToolbarItemIdentifier(id))

	img := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(sfSymbol, label)
	menuItem.SetImage(&img)

	menuItem.SetLabel(label)
	menuItem.SetPaletteLabel(label)
	menuItem.SetToolTip(label)
	menuItem.SetBordered(true)
	menuItem.SetAutovalidates(false)
	menuItem.SetShowsIndicator(true)

	menu := appkit.NewMenuWithTitle(label)
	addToolbarMenuItem(menu, "Boot to Recovery", "bootRecovery:", t.delegateID)
	addToolbarMenuSeparator(menu)
	addToolbarMenuItem(menu, "Suspend", "suspendVM:", t.delegateID)
	menuItem.SetMenu(&menu)

	return menuItem
}

// createSharedFolderMenu creates the Shared Folder dropdown toolbar item.
func (t *VMToolbar) createSharedFolderMenu() appkit.NSMenuToolbarItem {
	menuItem := appkit.NewMenuToolbarItemWithItemIdentifier(appkit.NSToolbarItemIdentifier(toolbarIDSharedFolder))

	img := appkit.NewImageWithSystemSymbolNameAccessibilityDescription("folder", "Shared Folders")
	menuItem.SetImage(&img)

	menuItem.SetLabel("Shared Folders")
	menuItem.SetPaletteLabel("Shared Folders")
	menuItem.SetToolTip("Shared Folders")
	menuItem.SetBordered(true)
	menuItem.SetAutovalidates(false)
	menuItem.SetShowsIndicator(true)

	menu := appkit.NewMenuWithTitle("Shared Folders")
	t.populateSharedFolderMenu(menu)
	menuItem.SetMenu(&menu)

	return menuItem
}

// populateSharedFolderMenu fills a shared-folder menu with the current folder
// list and an "Add Folder..." action. Used by both the toolbar dropdown and
// the menuNeedsUpdate: delegate for the main menu and status item submenus.
func (t *VMToolbar) populateSharedFolderMenu(menu appkit.NSMenu) {
	folders := LoadSharedFolders(t.vmDirectory)
	if len(folders) > 0 {
		for i, f := range folders {
			label := filepath.Base(f.Path)
			if f.ReadOnly {
				label += " (read-only)"
			}
			item := appkit.NewMenuItemWithTitleActionKeyEquivalent(label, 0, "")
			item.SetToolTip(f.Path)
			item.SetAction(objc.Sel("removeSharedFolder:"))
			item.SetTarget(objectivec.ObjectFromID(t.delegateID))
			item.SetTag(sharedFolderMenuTag + i)
			folderImg := appkit.NewImageWithSystemSymbolNameAccessibilityDescription("folder.fill", label)
			item.SetImage(&folderImg)
			menu.AddItem(&item)
		}
		addToolbarMenuSeparator(menu)
		addToolbarMenuItem(menu, "Remove All", "removeAllSharedFolders:", t.delegateID)
		addToolbarMenuSeparator(menu)
	}
	addToolbarMenuItem(menu, "Add Folder...", "addSharedFolder:", t.delegateID)
}

// handleMenuNeedsUpdate rebuilds a shared folder menu each time it opens.
// This fires for the main menu bar and status item submenus (which use the
// toolbar delegate as their NSMenuDelegate). The toolbar dropdown itself
// is rebuilt explicitly in refreshSharedFolderToolbarMenu.
func (t *VMToolbar) handleMenuNeedsUpdate(_ objc.ID, _ objc.SEL, menuID objc.ID) {
	menu := appkit.NSMenuFromID(menuID)
	objc.Send[struct{}](menu.ID, objc.Sel("removeAllItems"))
	t.populateSharedFolderMenu(menu)
}

// refreshSharedFolderToolbarMenu rebuilds the toolbar dropdown menu after
// folders are added or removed. NSMenuToolbarItem does not use
// menuNeedsUpdate:, so the menu must be rebuilt explicitly.
func (t *VMToolbar) refreshSharedFolderToolbarMenu() {
	item, ok := t.items[toolbarIDSharedFolder]
	if !ok {
		return
	}
	menuToolbarItem := appkit.NSMenuToolbarItemFromID(item.ID)
	menu := appkit.NewMenuWithTitle("Shared Folders")
	t.populateSharedFolderMenu(menu)
	menuToolbarItem.SetMenu(&menu)
}

// addToolbarMenuItem adds a menu item with the given action and target.
func addToolbarMenuItem(menu appkit.NSMenu, title, action string, target objc.ID) {
	var sel objc.SEL
	if action != "" {
		sel = objc.Sel(action)
	}
	item := appkit.NewMenuItemWithTitleActionKeyEquivalent(title, sel, "")
	if target != 0 {
		item.SetTarget(objectivec.ObjectFromID(target))
	}
	menu.AddItem(&item)
}

// addToolbarMenuSeparator adds a separator to an NSMenu.
func addToolbarMenuSeparator(menu appkit.NSMenu) {
	sepID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSMenuItem")),
		objc.Sel("separatorItem"),
	)
	sep := appkit.NSMenuItemFromID(sepID)
	menu.AddItem(&sep)
}

// UpdateState enables/disables toolbar items and swaps the play/pause icon
// based on the current VM state. Must be called on the main thread.
func (t *VMToolbar) UpdateState(state vz.VZVirtualMachineState) {
	running := state == vz.VZVirtualMachineStateRunning
	paused := state == vz.VZVirtualMachineStatePaused
	stopped := state == vz.VZVirtualMachineStateStopped
	busy := state == vz.VZVirtualMachineStateStarting ||
		state == vz.VZVirtualMachineStateStopping ||
		state == vz.VZVirtualMachineStateSaving ||
		state == vz.VZVirtualMachineStateRestoring

	// During installation, disable all VM control items.
	// Only screenshot and shared folder remain usable.
	if t.installing {
		for id, item := range t.items {
			switch id {
			case toolbarIDScreenshot:
				item.SetEnabled(running)
			case toolbarIDSharedFolder:
				item.SetEnabled(true)
			default:
				item.SetEnabled(false)
			}
		}
		return
	}

	if item, ok := t.items[toolbarIDStop]; ok {
		item.SetEnabled(running || paused)
	}
	if item, ok := t.items[toolbarIDStartPause]; ok {
		item.SetEnabled(!busy)
		if running {
			img := appkit.NewImageWithSystemSymbolNameAccessibilityDescription("pause.fill", "Pause")
			item.SetImage(&img)
			item.SetLabel("Pause")
			item.SetToolTip("Pause")
		} else {
			img := appkit.NewImageWithSystemSymbolNameAccessibilityDescription("play.fill", "Start")
			item.SetImage(&img)
			item.SetLabel("Start")
			item.SetToolTip("Start")
		}
	}
	if item, ok := t.items[toolbarIDRestart]; ok {
		item.SetEnabled(running)
	}
	if item, ok := t.items[toolbarIDBootOptions]; ok {
		item.SetEnabled((running || paused || stopped) && !busy)
	}
	if item, ok := t.items[toolbarIDCaptureInput]; ok {
		item.SetEnabled(running)
		if !running && t.captureEnabled {
			t.captureEnabled = false
			img := appkit.NewImageWithSystemSymbolNameAccessibilityDescription("keyboard", "Capture Input")
			item.SetImage(&img)
			item.SetLabel("Capture Input")
			item.SetToolTip("Capture Input")
		}
	}
	if item, ok := t.items[toolbarIDScreenshot]; ok {
		item.SetEnabled(running)
	}
	if item, ok := t.items[toolbarIDSharedFolder]; ok {
		item.SetEnabled(!busy)
	}
}

// Action handlers

func (t *VMToolbar) handleStop(_ objc.ID, _ objc.SEL, _ objc.ID) {
	requestVMStop("Toolbar", t.vm, t.vmQueue)
}

func (t *VMToolbar) handleStartPause(_ objc.ID, _ objc.SEL, _ objc.ID) {
	toggleVMStartPause("Toolbar", t.vm, t.vmQueue)
}

func (t *VMToolbar) handleRestart(_ objc.ID, _ objc.SEL, _ objc.ID) {
	restartVM("Toolbar", t.vm, t.vmQueue)
}

func (t *VMToolbar) handleBootRecovery(_ objc.ID, _ objc.SEL, _ objc.ID) {
	bootVMToRecovery("Toolbar", t.vm, t.vmQueue)
}

func (t *VMToolbar) handleSuspend(_ objc.ID, _ objc.SEL, _ objc.ID) {
	requestVMSuspend("Toolbar", t.vm, t.vmQueue)
}

func (t *VMToolbar) handleShowWindow(_ objc.ID, _ objc.SEL, _ objc.ID) {
	t.window.MakeKeyAndOrderFront(nil)
	app := getSharedApp()
	app.Activate()
}

func (t *VMToolbar) handleCaptureInput(_ objc.ID, _ objc.SEL, _ objc.ID) {
	t.captureEnabled = !t.captureEnabled
	t.vmView.SetCapturesSystemKeys(t.captureEnabled)

	if item, ok := t.items[toolbarIDCaptureInput]; ok {
		symbol := "keyboard"
		label := "Capture Input"
		if t.captureEnabled {
			symbol = "keyboard.fill"
			label = "Release Input"
		}
		img := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(symbol, label)
		item.SetImage(&img)
		item.SetLabel(label)
		item.SetToolTip(label)
	}

	if t.captureEnabled {
		fmt.Println("Toolbar: system key capture enabled")
	} else {
		fmt.Println("Toolbar: system key capture disabled")
	}
}

func (t *VMToolbar) handleScreenshot(_ objc.ID, _ objc.SEL, _ objc.ID) {
	saveCurrentVMScreenshot("Toolbar", t.screenshots)
}

// handleAddSharedFolder opens an NSOpenPanel to pick a directory,
// adds it to shared_folders.json, and hotplugs it into the running VM.
func (t *VMToolbar) handleAddSharedFolder(_ objc.ID, _ objc.SEL, _ objc.ID) {
	panel := appkit.NewNSOpenPanel()
	panel.SetCanChooseDirectories(true)
	panel.SetCanChooseFiles(false)
	panel.SetAllowsMultipleSelection(true)
	panel.SetMessage("Choose folders to share with the VM")

	response := panel.NSSavePanel.RunModal()
	if !isModalResponseOK(response) {
		return
	}

	urls := panel.URLs()
	if len(urls) == 0 {
		return
	}

	folders := LoadSharedFolders(t.vmDirectory)
	changed := false
	for _, u := range urls {
		dirPath := u.Path()
		if dirPath == "" {
			continue
		}
		// Skip duplicates
		dup := false
		for _, f := range folders {
			if f.Path == dirPath {
				dup = true
				break
			}
		}
		if dup {
			fmt.Printf("Toolbar: folder already shared: %s\n", dirPath)
			continue
		}
		tag := uniqueTag(filepath.Base(dirPath), folders)
		folders = append(folders, SharedFolderEntry{
			Path:     dirPath,
			Tag:      tag,
			ReadOnly: false,
		})
		fmt.Printf("Toolbar: shared folder added: %s (tag: %s)\n", dirPath, tag)
		changed = true
	}

	if !changed {
		return
	}

	t.saveAndApplySharedFolders(folders)
}

// handleRemoveSharedFolder removes a specific shared folder identified by menu item tag.
func (t *VMToolbar) handleRemoveSharedFolder(_ objc.ID, _ objc.SEL, senderID objc.ID) {
	tag := int(objc.Send[int64](senderID, objc.Sel("tag")))
	idx := tag - sharedFolderMenuTag
	folders := LoadSharedFolders(t.vmDirectory)
	if idx < 0 || idx >= len(folders) {
		return
	}

	removed := folders[idx]
	folders = append(folders[:idx], folders[idx+1:]...)
	fmt.Printf("Toolbar: removed shared folder: %s\n", removed.Path)
	t.saveAndApplySharedFolders(folders)
}

// handleRemoveAllSharedFolders removes all shared folders.
func (t *VMToolbar) handleRemoveAllSharedFolders(_ objc.ID, _ objc.SEL, _ objc.ID) {
	fmt.Println("Toolbar: removing all shared folders")
	t.saveAndApplySharedFolders(nil)
}

// saveAndApplySharedFolders persists the folder list and hotplugs into the running VM.
func (t *VMToolbar) saveAndApplySharedFolders(folders []SharedFolderEntry) {
	if err := saveSharedFolders(t.vmDirectory, folders); err != nil {
		fmt.Fprintf(os.Stderr, "error: saving shared folders: %v\n", err)
		return
	}

	// Refresh the toolbar dropdown menu (it doesn't use menuNeedsUpdate:).
	t.refreshSharedFolderToolbarMenu()

	// Hotplug: update the running VM's directory sharing device
	t.applySharedFoldersToVM(folders)

	// Mirror CLI behavior: after hotplugging shares, ensure the guest mount
	// exists so folders appear immediately without requiring a reboot.
	if len(folders) > 0 && !linuxMode {
		go t.ensureGuestSharedFoldersMounted()
	}
}

// applySharedFoldersToVM updates the VirtioFS share on a running VM.
// It finds the first directory sharing device and sets a VZMultipleDirectoryShare
// containing all configured folders.
func (t *VMToolbar) applySharedFoldersToVM(folders []SharedFolderEntry) {
	go func() {
		applied, err := newSharedFolderRuntimeApplier(t.vm, t.vmQueue).Apply(folders)
		if err != nil {
			fmt.Printf("Toolbar: %v\n", err)
			return
		}
		if applied == 0 {
			fmt.Println("Toolbar: cleared all shared folders on running VM")
			return
		}
		fmt.Printf("Toolbar: hotplugged %d shared folder(s) into running VM\n", applied)
	}()
}

func (t *VMToolbar) ensureGuestSharedFoldersMounted() {
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		mounted, err := mountSharedFoldersInGuest(t.vmDirectory, defaultSharedFoldersMountPoint)
		if err == nil {
			if mounted {
				fmt.Printf("Toolbar: mounted shared folders at %s\n", defaultSharedFoldersMountPoint)
			} else if verbose {
				fmt.Printf("Toolbar: shared folders already mounted at %s\n", defaultSharedFoldersMountPoint)
			}
			return
		}
		lastErr = err
		// Agent/socket readiness often lags behind a recent boot; retry shortly.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "guest agent unavailable") ||
			strings.Contains(msg, "not ready") ||
			strings.Contains(msg, "starting") ||
			strings.Contains(msg, "resuming") {
			time.Sleep(2 * time.Second)
			continue
		}
		break
	}
	fmt.Printf("Toolbar: could not mount shared folders in guest: %v\n", lastErr)
}
