package main

import (
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
)

// setupMainMenu creates a proper macOS main menu bar with standard menus.
// The toolbar delegate is used as the target for VM-specific actions.
func setupMainMenu(toolbarDelegate objc.ID) {
	app := getSharedApp()

	mainMenu := appkit.NewMenuWithTitle("")

	// App menu (macOS auto-populates the name from the process)
	appMenu := appkit.NewMenuWithTitle("")
	addMainMenuItem(appMenu, "About vz-macos", "orderFrontStandardAboutPanel:", "", 0)
	addMainMenuSeparator(appMenu)
	addMainMenuItem(appMenu, "Quit vz-macos", "terminate:", "q", 0)
	appMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("", 0, "")
	appMenuItem.SetSubmenu(&appMenu)
	mainMenu.AddItem(&appMenuItem)

	// Edit menu (standard responder chain — nil target)
	editMenu := appkit.NewMenuWithTitle("Edit")
	addMainMenuItem(editMenu, "Undo", "undo:", "z", 0)
	addMainMenuItemWithModifiers(editMenu, "Redo", "redo:", "z", 0,
		appkit.NSEventModifierFlagCommand|appkit.NSEventModifierFlagShift)
	addMainMenuSeparator(editMenu)
	addMainMenuItem(editMenu, "Cut", "cut:", "x", 0)
	addMainMenuItem(editMenu, "Copy", "copy:", "c", 0)
	addMainMenuItem(editMenu, "Paste", "paste:", "v", 0)
	addMainMenuItem(editMenu, "Select All", "selectAll:", "a", 0)
	editMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Edit", 0, "")
	editMenuItem.SetSubmenu(&editMenu)
	mainMenu.AddItem(&editMenuItem)

	// VM menu (uses toolbar delegate as target)
	vmMenu := appkit.NewMenuWithTitle("VM")
	addMainMenuItem(vmMenu, "Stop", "stopVM:", ".", toolbarDelegate)
	addMainMenuItem(vmMenu, "Pause", "startPauseVM:", "p", toolbarDelegate)
	addMainMenuItem(vmMenu, "Restart", "restartVM:", "r", toolbarDelegate)
	addMainMenuItem(vmMenu, "Suspend", "suspendVM:", "", toolbarDelegate)
	// Boot Options submenu
	bootMenu := appkit.NewMenuWithTitle("Boot Options")
	addMainMenuItem(bootMenu, "Boot to Recovery", "bootRecovery:", "", toolbarDelegate)
	bootMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Boot Options", 0, "")
	bootMenuItem.SetSubmenu(&bootMenu)
	vmMenu.AddItem(&bootMenuItem)
	addMainMenuSeparator(vmMenu)
	addMainMenuItem(vmMenu, "Capture Input", "captureInput:", "k", toolbarDelegate)
	addMainMenuItem(vmMenu, "Screenshot...", "takeScreenshot:", "s", toolbarDelegate)
	// Shared Folders submenu
	sharedMenu := appkit.NewMenuWithTitle("Shared Folders")
	// Set the delegate so menuNeedsUpdate: fires before each open
	delegateObj := appkit.NSMenuDelegateObjectFromID(toolbarDelegate)
	sharedMenu.SetDelegate(delegateObj)
	addMainMenuItem(sharedMenu, "Add Folder...", "addSharedFolder:", "", toolbarDelegate)
	sharedMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Shared Folders", 0, "")
	sharedMenuItem.SetSubmenu(&sharedMenu)
	vmMenu.AddItem(&sharedMenuItem)
	vmMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("VM", 0, "")
	vmMenuItem.SetSubmenu(&vmMenu)
	mainMenu.AddItem(&vmMenuItem)

	// View menu
	viewMenu := appkit.NewMenuWithTitle("View")
	addMainMenuItem(viewMenu, "Toggle Toolbar", "toggleToolbarShown:", "t", 0)
	addMainMenuSeparator(viewMenu)
	addMainMenuItemWithModifiers(viewMenu, "Enter Full Screen", "toggleFullScreen:", "f", 0,
		appkit.NSEventModifierFlagCommand|appkit.NSEventModifierFlagControl)
	viewMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("View", 0, "")
	viewMenuItem.SetSubmenu(&viewMenu)
	mainMenu.AddItem(&viewMenuItem)

	// Window menu
	windowMenu := appkit.NewMenuWithTitle("Window")
	addMainMenuItem(windowMenu, "Minimize", "performMiniaturize:", "m", 0)
	addMainMenuItem(windowMenu, "Zoom", "performZoom:", "", 0)
	addMainMenuSeparator(windowMenu)
	addMainMenuItem(windowMenu, "Bring All to Front", "arrangeInFront:", "", 0)
	windowMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Window", 0, "")
	windowMenuItem.SetSubmenu(&windowMenu)
	mainMenu.AddItem(&windowMenuItem)

	// Set the window menu so macOS tracks windows automatically
	objc.Send[objc.ID](app.ID, objc.Sel("setWindowsMenu:"), windowMenu.ID)

	// Set the main menu
	objc.Send[objc.ID](app.ID, objc.Sel("setMainMenu:"), mainMenu.ID)
}

// addMainMenuItem adds a menu item. If target is 0 (nil), uses first responder chain.
func addMainMenuItem(menu appkit.NSMenu, title, action, key string, target objc.ID) {
	var sel objc.SEL
	if action != "" {
		sel = objc.Sel(action)
	}
	item := appkit.NewMenuItemWithTitleActionKeyEquivalent(title, sel, key)
	if target != 0 {
		item.SetTarget(objectivec.ObjectFromID(target))
	}
	menu.AddItem(&item)
}

// addMainMenuItemWithModifiers adds a menu item with explicit modifier flags.
func addMainMenuItemWithModifiers(menu appkit.NSMenu, title, action, key string, target objc.ID, modifiers appkit.NSEventModifierFlags) {
	var sel objc.SEL
	if action != "" {
		sel = objc.Sel(action)
	}
	item := appkit.NewMenuItemWithTitleActionKeyEquivalent(title, sel, key)
	item.SetKeyEquivalentModifierMask(modifiers)
	if target != 0 {
		item.SetTarget(objectivec.ObjectFromID(target))
	}
	menu.AddItem(&item)
}

// setupSelectorMenu creates the menu bar for the VM selector window.
func setupSelectorMenu(selectorTarget objc.ID) {
	app := getSharedApp()

	mainMenu := appkit.NewMenuWithTitle("")

	// App menu
	appMenu := appkit.NewMenuWithTitle("")
	addMainMenuItem(appMenu, "About vz-macos", "orderFrontStandardAboutPanel:", "", 0)
	addMainMenuSeparator(appMenu)
	addMainMenuItem(appMenu, "Quit vz-macos", "terminate:", "q", 0)
	appMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("", 0, "")
	appMenuItem.SetSubmenu(&appMenu)
	mainMenu.AddItem(&appMenuItem)

	// Scripts menu
	scriptsMenu := appkit.NewMenuWithTitle("Scripts")
	addMainMenuItem(scriptsMenu, "Run VZScript...", "openVZScriptRunner:", "", selectorTarget)
	scriptsMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Scripts", 0, "")
	scriptsMenuItem.SetSubmenu(&scriptsMenu)
	mainMenu.AddItem(&scriptsMenuItem)

	// Window menu
	windowMenu := appkit.NewMenuWithTitle("Window")
	addMainMenuItem(windowMenu, "Minimize", "performMiniaturize:", "m", 0)
	addMainMenuItem(windowMenu, "Zoom", "performZoom:", "", 0)
	windowMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Window", 0, "")
	windowMenuItem.SetSubmenu(&windowMenu)
	mainMenu.AddItem(&windowMenuItem)

	objc.Send[objc.ID](app.ID, objc.Sel("setWindowsMenu:"), windowMenu.ID)
	objc.Send[objc.ID](app.ID, objc.Sel("setMainMenu:"), mainMenu.ID)
}

// addMainMenuSeparator adds a separator item to a menu.
func addMainMenuSeparator(menu appkit.NSMenu) {
	sepID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSMenuItem")),
		objc.Sel("separatorItem"),
	)
	sep := appkit.NSMenuItemFromID(sepID)
	menu.AddItem(&sep)
}
