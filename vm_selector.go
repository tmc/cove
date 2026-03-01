package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
)

// VM selector window dimensions.
const (
	selectorWindowWidth  = 540
	selectorWindowHeight = 400
	selectorMinWidth     = 400
	selectorMinHeight    = 300
	selectorTableHeight  = 340
	selectorBarHeight    = 50
	selectorButtonHeight = 30
)

// VMSelector displays a native macOS window with a table of VMs.
type VMSelector struct {
	window     appkit.NSWindow
	tableView  appkit.NSTableView
	runButton  appkit.NSButton
	delButton  appkit.NSButton
	vms        []VMInfo
	activeVM   string
	delegateID objc.ID
	onSelect   func(VMInfo)
	onInstall  func()
}

// NewVMSelector creates and configures the VM selector window.
func NewVMSelector(vms []VMInfo, onSelect func(VMInfo), onInstall func()) *VMSelector {
	s := &VMSelector{
		vms:       vms,
		activeVM:  GetActiveVM(),
		onSelect:  onSelect,
		onInstall: onInstall,
	}
	s.registerDelegate()
	s.buildWindow()
	return s
}

// Show displays the selector window.
func (s *VMSelector) Show() {
	s.window.MakeKeyAndOrderFront(nil)
	app := getSharedApp()
	app.Activate()
}

// registerDelegate registers the ObjC class for NSTableView data source and delegate.
func (s *VMSelector) registerDelegate() {
	cls, err := objc.RegisterClass(
		"VMSelectorDelegate",
		objc.GetClass("NSObject"),
		nil, nil,
		[]objc.MethodDef{
			// NSTableViewDataSource
			{Cmd: objc.RegisterName("numberOfRowsInTableView:"), Fn: s.numberOfRows},
			{Cmd: objc.RegisterName("tableView:objectValueForTableColumn:row:"), Fn: s.objectValueForColumn},
			// NSTableViewDelegate
			{Cmd: objc.RegisterName("tableViewSelectionDidChange:"), Fn: s.selectionDidChange},
			// Button actions
			{Cmd: objc.RegisterName("runVM:"), Fn: s.handleRun},
			{Cmd: objc.RegisterName("createVM:"), Fn: s.handleCreate},
			{Cmd: objc.RegisterName("deleteVM:"), Fn: s.handleDelete},
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not register VMSelectorDelegate: %v\n", err)
		return
	}
	s.delegateID = objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	s.delegateID = objc.Send[objc.ID](s.delegateID, objc.Sel("init"))
}

// buildWindow creates the NSWindow, NSTableView, and buttons.
func (s *VMSelector) buildWindow() {
	contentRect := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 200, Y: 200},
		Size:   corefoundation.CGSize{Width: selectorWindowWidth, Height: selectorWindowHeight},
	}
	s.window = appkit.NewWindowWithContentRectStyleMaskBackingDefer(
		contentRect,
		appkit.NSWindowStyleMaskTitled|
			appkit.NSWindowStyleMaskClosable|
			appkit.NSWindowStyleMaskMiniaturizable|
			appkit.NSWindowStyleMaskResizable,
		appkit.NSBackingStoreBuffered,
		false,
	)
	s.window.SetTitle("vz-macos")
	s.window.SetMinSize(corefoundation.CGSize{Width: selectorMinWidth, Height: selectorMinHeight})
	s.window.Center()
	objc.Send[objc.ID](s.window.ID, objc.Sel("setReleasedWhenClosed:"), false)

	// Build the table view
	tableFrame := corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: selectorWindowWidth, Height: selectorTableHeight},
	}
	s.tableView = appkit.NewTableViewWithFrame(tableFrame)
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("retain"))

	// Configure cell-based table view
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setUsesAlternatingRowBackgroundColors:"), true)
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setAllowsEmptySelection:"), false)

	// Set data source and delegate
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setDataSource:"), s.delegateID)
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setDelegate:"), s.delegateID)

	// Double-click action
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setTarget:"), s.delegateID)
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setDoubleAction:"), objc.Sel("runVM:"))

	// Add columns
	s.addColumn("name", "Name", 250, true)
	s.addColumn("os", "OS", 60, false)
	s.addColumn("size", "Size", 80, false)
	s.addColumn("created", "Created", 100, false)

	// Wrap table in scroll view
	scrollView := appkit.NewScrollViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: selectorBarHeight},
		Size:   corefoundation.CGSize{Width: selectorWindowWidth, Height: selectorWindowHeight - selectorBarHeight},
	})
	objc.Send[objc.ID](scrollView.ID, objc.Sel("retain"))
	objc.Send[objc.ID](scrollView.ID, objc.Sel("setDocumentView:"), s.tableView.ID)
	objc.Send[objc.ID](scrollView.ID, objc.Sel("setHasVerticalScroller:"), true)
	// NSViewWidthSizable (2) | NSViewHeightSizable (16)
	objc.Send[objc.ID](scrollView.ID, objc.Sel("setAutoresizingMask:"), uint(2|16))

	// Build button bar
	buttonBar := s.buildButtonBar()

	// Add subviews to the window's content view
	contentViewID := objc.Send[objc.ID](s.window.ID, objc.Sel("contentView"))
	objc.Send[objc.ID](contentViewID, objc.Sel("addSubview:"), scrollView.ID)
	objc.Send[objc.ID](contentViewID, objc.Sel("addSubview:"), buttonBar)

	// Select first row if available
	if len(s.vms) > 0 {
		indexSet := objc.Send[objc.ID](
			objc.Send[objc.ID](objc.ID(objc.GetClass("NSIndexSet")), objc.Sel("alloc")),
			objc.Sel("initWithIndex:"), uint(0),
		)
		objc.Send[objc.ID](s.tableView.ID, objc.Sel("selectRowIndexes:byExtendingSelection:"), indexSet, false)
	}

	s.updateButtonStates()
}

// addColumn adds an NSTableColumn to the table view.
func (s *VMSelector) addColumn(identifier, title string, width float64, resizable bool) {
	col := appkit.NewTableColumnWithIdentifier(appkit.NSUserInterfaceItemIdentifier(identifier))
	col.SetTitle(title)
	col.SetWidth(width)
	if resizable {
		col.SetResizingMask(appkit.NSTableColumnAutoresizingMask | appkit.NSTableColumnUserResizingMask)
	} else {
		col.SetMinWidth(width)
		col.SetMaxWidth(width + 40)
		col.SetResizingMask(appkit.NSTableColumnUserResizingMask)
	}
	objc.Send[objc.ID](col.ID, objc.Sel("retain"))
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("addTableColumn:"), col.ID)
}

// buildButtonBar creates the bottom button bar with New VM, Delete, and Run buttons.
// Returns the raw objc.ID of the bar view.
func (s *VMSelector) buildButtonBar() objc.ID {
	barFrame := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   corefoundation.CGSize{Width: selectorWindowWidth, Height: selectorBarHeight},
	}
	bar := appkit.NewViewWithFrame(barFrame)
	objc.Send[objc.ID](bar.ID, objc.Sel("retain"))
	// NSViewWidthSizable (2)
	objc.Send[objc.ID](bar.ID, objc.Sel("setAutoresizingMask:"), uint(2))

	target := objectivec.ObjectFromID(s.delegateID)

	// "+ New VM..." button (left-aligned)
	newBtn := appkit.NewButtonWithTitleTargetAction("+ New VM...", target, objc.Sel("createVM:"))
	newBtn.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 12, Y: 10},
		Size:   corefoundation.CGSize{Width: 100, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](newBtn.ID, objc.Sel("setBezelStyle:"), int(1)) // NSBezelStyleRounded
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), newBtn.ID)

	// "Run" button (right-aligned, default button)
	s.runButton = appkit.NewButtonWithTitleTargetAction("Run", target, objc.Sel("runVM:"))
	s.runButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: selectorWindowWidth - 12 - 80, Y: 10},
		Size:   corefoundation.CGSize{Width: 80, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.runButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](s.runButton.ID, objc.Sel("setKeyEquivalent:"), objc.String("\r"))
	// NSViewMinXMargin (4) — stay anchored to right edge
	objc.Send[objc.ID](s.runButton.ID, objc.Sel("setAutoresizingMask:"), uint(4))
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), s.runButton.ID)

	// "Delete" button (right-aligned, before Run)
	s.delButton = appkit.NewButtonWithTitleTargetAction("Delete", target, objc.Sel("deleteVM:"))
	s.delButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: selectorWindowWidth - 12 - 80 - 8 - 80, Y: 10},
		Size:   corefoundation.CGSize{Width: 80, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.delButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](s.delButton.ID, objc.Sel("setAutoresizingMask:"), uint(4))
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), s.delButton.ID)

	return bar.ID
}

// updateButtonStates enables/disables buttons based on selection.
func (s *VMSelector) updateButtonStates() {
	row := int(objc.Send[int64](s.tableView.ID, objc.Sel("selectedRow")))
	hasSelection := row >= 0 && row < len(s.vms)
	objc.Send[objc.ID](s.runButton.ID, objc.Sel("setEnabled:"), hasSelection)

	// Disable Delete for active VM
	canDelete := hasSelection && s.vms[row].Name != s.activeVM
	objc.Send[objc.ID](s.delButton.ID, objc.Sel("setEnabled:"), canDelete)
}

// selectedVM returns the currently selected VM, or nil if none.
func (s *VMSelector) selectedVM() *VMInfo {
	row := int(objc.Send[int64](s.tableView.ID, objc.Sel("selectedRow")))
	if row < 0 || row >= len(s.vms) {
		return nil
	}
	return &s.vms[row]
}

// NSTableViewDataSource: numberOfRowsInTableView:
func (s *VMSelector) numberOfRows(_ objc.ID, _ objc.SEL, _ objc.ID) int {
	return len(s.vms)
}

// NSTableViewDataSource: tableView:objectValueForTableColumn:row:
func (s *VMSelector) objectValueForColumn(_ objc.ID, _ objc.SEL, _ objc.ID, colID objc.ID, row int) objc.ID {
	if row < 0 || row >= len(s.vms) {
		return 0
	}
	vm := s.vms[row]

	// Get column identifier
	identifier := foundation.NSStringFromID(
		objc.Send[objc.ID](colID, objc.Sel("identifier")),
	).String()

	var value string
	switch identifier {
	case "name":
		value = vm.Name
		if vm.Name == s.activeVM {
			value += " *"
		}
	case "os":
		value = vm.OSType
	case "size":
		value = FormatSize(vm.DiskSize)
	case "created":
		value = vm.Created.Format("2006-01-02")
	}
	return objc.String(value)
}

// NSTableViewDelegate: tableViewSelectionDidChange:
func (s *VMSelector) selectionDidChange(_ objc.ID, _ objc.SEL, _ objc.ID) {
	s.updateButtonStates()
}

// handleRun runs the selected VM.
func (s *VMSelector) handleRun(_ objc.ID, _ objc.SEL, _ objc.ID) {
	vm := s.selectedVM()
	if vm == nil {
		return
	}
	if s.onSelect != nil {
		s.onSelect(*vm)
	}
}

// handleCreate starts the install flow for a new VM.
func (s *VMSelector) handleCreate(_ objc.ID, _ objc.SEL, _ objc.ID) {
	if s.onInstall != nil {
		s.onInstall()
	}
}

// handleDelete deletes the selected VM after confirmation.
func (s *VMSelector) handleDelete(_ objc.ID, _ objc.SEL, _ objc.ID) {
	vm := s.selectedVM()
	if vm == nil {
		return
	}

	// Show confirmation alert
	alertID := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("NSAlert")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	objc.Send[objc.ID](alertID, objc.Sel("setMessageText:"),
		objc.String(fmt.Sprintf("Delete VM \"%s\"?", vm.Name)))
	objc.Send[objc.ID](alertID, objc.Sel("setInformativeText:"),
		objc.String("This will permanently delete the VM and its disk image. This action cannot be undone."))
	objc.Send[objc.ID](alertID, objc.Sel("setAlertStyle:"), int(2)) // NSAlertStyleCritical
	objc.Send[objc.ID](alertID, objc.Sel("addButtonWithTitle:"), objc.String("Delete"))
	objc.Send[objc.ID](alertID, objc.Sel("addButtonWithTitle:"), objc.String("Cancel"))

	response := objc.Send[int64](alertID, objc.Sel("runModal"))
	if response != 1000 { // NSAlertFirstButtonReturn
		return
	}

	if err := DeleteVM(vm.Name); err != nil {
		fmt.Fprintf(os.Stderr, "error deleting VM: %v\n", err)
		return
	}

	// Refresh the VM list
	newVMs, err := ListVMs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing VMs: %v\n", err)
		return
	}
	s.vms = newVMs
	s.activeVM = GetActiveVM()
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("reloadData"))

	// Re-select first row
	if len(s.vms) > 0 {
		indexSet := objc.Send[objc.ID](
			objc.Send[objc.ID](objc.ID(objc.GetClass("NSIndexSet")), objc.Sel("alloc")),
			objc.Sel("initWithIndex:"), uint(0),
		)
		objc.Send[objc.ID](s.tableView.ID, objc.Sel("selectRowIndexes:byExtendingSelection:"), indexSet, false)
	}
	s.updateButtonStates()
}

// showVMSelectorWindow creates and shows the VM selector as the main application window.
func showVMSelectorWindow(vms []VMInfo) {
	transformToForegroundApp()

	app := getSharedApp()
	app.SetActivationPolicy(appkit.NSApplicationActivationPolicyRegular)
	setAppIcon(&app)

	if !appFinishedLaunching {
		foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(0, false, func(_ *foundation.NSTimer) {
			app.Stop(nil)
			postDummyEvent(app)
		})
		app.Run()
		appFinishedLaunching = true
	}

	// Declare selector before the closures that reference it.
	var selector *VMSelector
	selector = NewVMSelector(vms, func(vm VMInfo) {
		// Close selector window, then run the selected VM
		objc.Send[objc.ID](selector.window.ID, objc.Sel("close"))
		app.Stop(nil)
		postDummyEvent(app)

		vmDir = vm.Path
		vmName = vm.Name
		guiMode = true
		if vm.OSType == "Linux" {
			linuxMode = true
		}
		handleRun()
	}, func() {
		// Close selector window, then start install flow
		objc.Send[objc.ID](selector.window.ID, objc.Sel("close"))
		app.Stop(nil)
		postDummyEvent(app)

		guiMode = true
		if err := installMacOSLikeVZ(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	})

	setupSelectorMenu()

	// Quit when the selector window closes.
	nc := foundation.GetNotificationCenterClass().DefaultCenter()
	nsName := objc.String("NSWindowWillCloseNotification")
	objc.Send[objc.ID](nc.ID, objc.Sel("addObserverForName:object:queue:usingBlock:"),
		nsName, selector.window.ID, objc.ID(0),
		objc.NewBlock(func(_ objc.Block, _ objc.ID) {
			app.Stop(nil)
			postDummyEvent(app)
		}),
	)

	selector.Show()
	app.Run()
}
