package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	coldButton appkit.NSButton
	delButton  appkit.NSButton
	vms        []VMInfo
	activeVM   string
	delegateID objc.ID
	onSelect   func(VMInfo, bool)
	onInstall  func()
}

type selectorActionKind int

const (
	selectorActionNone selectorActionKind = iota
	selectorActionRun
	selectorActionInstall
)

type selectorAction struct {
	kind     selectorActionKind
	vm       VMInfo
	coldBoot bool
	newVM    newVMOptions
}

type newVMOptions struct {
	Name              string
	ProvisionUser     string
	ProvisionPassword string
	ProvisionAdmin    bool
}

func nextNewVMName() string {
	const base = "macos"
	name := base
	for i := 2; ; i++ {
		if _, err := os.Stat(GetVMPath(name)); os.IsNotExist(err) {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

func validateNewVMName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("enter a VM name")
	}
	if filepath.Base(name) != name || filepath.Clean(name) != name {
		return errors.New("vm name must not contain path separators")
	}
	if name == "." || name == ".." {
		return errors.New("choose a different VM name")
	}
	return nil
}

func showSelectorAlert(title, message string) {
	alert := appkit.NewNSAlert()
	alert.SetAlertStyle(appkit.NSAlertStyleWarning)
	alert.SetMessageText(title)
	alert.SetInformativeText(message)
	alert.AddButtonWithTitle("OK")
	alert.RunModal()
}

func showSelectorError(title string, err error) {
	if err == nil {
		return
	}
	alert := appkit.NewNSAlert()
	alert.SetAlertStyle(appkit.NSAlertStyleCritical)
	alert.SetMessageText(title)
	alert.SetInformativeText(err.Error())
	alert.AddButtonWithTitle("OK")
	alert.RunModal()
}

func validateNewVMOptions(opts newVMOptions) error {
	if err := validateNewVMName(opts.Name); err != nil {
		return err
	}
	if opts.ProvisionUser == "" {
		if strings.TrimSpace(opts.ProvisionPassword) != "" {
			return errors.New("enter a username for provisioning")
		}
		return nil
	}
	if err := validateUsername(opts.ProvisionUser); err != nil {
		return err
	}
	if opts.ProvisionPassword == "" {
		return errors.New("enter a password for provisioning")
	}
	return nil
}

func newVMAccessoryView(opts newVMOptions) (appkit.NSView, appkit.NSTextField, appkit.NSTextField, appkit.NSSecureTextField) {
	view := appkit.NewViewWithFrame(corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: 320, Height: 104},
	})

	addLabel := func(text string, y float64) {
		label := appkit.NewTextFieldLabelWithString(text)
		label.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 0, Y: y},
			Size:   corefoundation.CGSize{Width: 92, Height: 22},
		})
		objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), label.ID)
	}

	addLabel("VM Name:", 76)
	addLabel("Username:", 42)
	addLabel("Password:", 8)

	nameField := appkit.NewTextFieldWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 96, Y: 72},
		Size:   corefoundation.CGSize{Width: 224, Height: 24},
	})
	nameField.SetStringValue(opts.Name)
	nameField.SetPlaceholderString("macos")
	nameField.SetEditable(true)
	nameField.SetSelectable(true)
	nameField.SetAccessibilityLabel("VM Name")
	nameField.SetAccessibilityIdentifier("new-vm-name")
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), nameField.ID)

	userField := appkit.NewTextFieldWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 96, Y: 38},
		Size:   corefoundation.CGSize{Width: 224, Height: 24},
	})
	userField.SetStringValue(opts.ProvisionUser)
	userField.SetPlaceholderString("optional")
	userField.SetEditable(true)
	userField.SetSelectable(true)
	userField.SetAccessibilityLabel("Username")
	userField.SetAccessibilityIdentifier("new-vm-username")
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), userField.ID)

	passwordField := appkit.NewSecureTextFieldWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 96, Y: 4},
		Size:   corefoundation.CGSize{Width: 224, Height: 24},
	})
	passwordField.SetStringValue(opts.ProvisionPassword)
	passwordField.SetPlaceholderString("optional")
	passwordField.SetEditable(true)
	passwordField.SetSelectable(true)
	passwordField.SetAccessibilityLabel("Password")
	passwordField.SetAccessibilityIdentifier("new-vm-password")
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), passwordField.ID)

	return view, nameField, userField, passwordField
}

func promptForNewVMOptions() (newVMOptions, bool) {
	baseDir := GetVMBaseDir()
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		showSelectorAlert("Cannot Create VM", fmt.Sprintf("Could not create %s: %v", baseDir, err))
		return newVMOptions{}, false
	}
	baseDir = resolvePath(baseDir)
	opts := newVMOptions{
		Name:              nextNewVMName(),
		ProvisionUser:     strings.TrimSpace(provisionUser),
		ProvisionPassword: provisionPassword,
		ProvisionAdmin:    true,
	}

	for {
		alert := appkit.NewNSAlert()
		alert.SetMessageText("Create New VM")
		alert.SetInformativeText(fmt.Sprintf("Enter a name for the new VM. It will be created in %s.\n\nLeave username and password blank to install without provisioning. If provided, the user will be created as an administrator.", baseDir))
		alert.AddButtonWithTitle("Create")
		alert.AddButtonWithTitle("Cancel")
		accessoryView, nameField, userField, passwordField := newVMAccessoryView(opts)
		alert.SetAccessoryView(accessoryView)

		if alert.RunModal() != appkit.AlertFirstButtonReturn {
			return newVMOptions{}, false
		}

		opts.Name = strings.TrimSpace(nameField.StringValue())
		opts.ProvisionUser = strings.TrimSpace(userField.StringValue())
		opts.ProvisionPassword = passwordField.StringValue()
		opts.ProvisionAdmin = true

		if err := validateNewVMOptions(opts); err != nil {
			showSelectorAlert("Invalid VM Settings", err.Error())
			continue
		}

		vmPath := GetVMPath(opts.Name)
		if _, err := os.Stat(vmPath); err == nil {
			showSelectorAlert("VM Already Exists", fmt.Sprintf("A VM named %q already exists.", opts.Name))
			continue
		} else if !os.IsNotExist(err) {
			showSelectorAlert("Cannot Create VM", fmt.Sprintf("Could not inspect %s: %v", vmPath, err))
			return newVMOptions{}, false
		}

		return opts, true
	}
}

func runButtonTitle(vm *VMInfo) string {
	if vm == nil {
		return "Run"
	}
	switch vm.State {
	case "running":
		return "Running"
	case "suspended":
		return "Resume"
	}
	return "Run"
}

func selectorStateText(vm VMInfo) string {
	switch vm.State {
	case "running":
		return "Running"
	case "suspended":
		return "Suspended"
	default:
		return "Stopped"
	}
}

func (s *VMSelector) initialSelectionRow() int {
	if s.activeVM != "" {
		for i, vm := range s.vms {
			if vm.Name == s.activeVM {
				return i
			}
		}
	}
	if len(s.vms) == 0 {
		return -1
	}
	return 0
}

// NewVMSelector creates and configures the VM selector window.
func NewVMSelector(vms []VMInfo, onSelect func(VMInfo, bool), onInstall func()) *VMSelector {
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
			{Cmd: objc.RegisterName("coldBootVM:"), Fn: s.handleColdBoot},
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
	s.addColumn("name", "Name", 190, true)
	s.addColumn("state", "State", 90, false)
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

	// Select the active VM when present, otherwise the first row.
	if row := s.initialSelectionRow(); row >= 0 {
		indexSet := objc.Send[objc.ID](
			objc.Send[objc.ID](objc.ID(objc.GetClass("NSIndexSet")), objc.Sel("alloc")),
			objc.Sel("initWithIndex:"), uint(row),
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
	// NSViewMinXMargin (4) — stay anchored to right edge
	objc.Send[objc.ID](s.runButton.ID, objc.Sel("setAutoresizingMask:"), uint(4))
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), s.runButton.ID)

	// "Cold Boot" button (right-aligned, before Run)
	s.coldButton = appkit.NewButtonWithTitleTargetAction("Cold Boot", target, objc.Sel("coldBootVM:"))
	s.coldButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: selectorWindowWidth - 12 - 80 - 8 - 100, Y: 10},
		Size:   corefoundation.CGSize{Width: 100, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.coldButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](s.coldButton.ID, objc.Sel("setAutoresizingMask:"), uint(4))
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), s.coldButton.ID)

	// "Delete" button (right-aligned, before Run)
	s.delButton = appkit.NewButtonWithTitleTargetAction("Delete", target, objc.Sel("deleteVM:"))
	s.delButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: selectorWindowWidth - 12 - 80 - 8 - 100 - 8 - 80, Y: 10},
		Size:   corefoundation.CGSize{Width: 80, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.delButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](s.delButton.ID, objc.Sel("setAutoresizingMask:"), uint(4))
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), s.delButton.ID)

	return bar.ID
}

// updateButtonStates enables/disables buttons based on selection.
func (s *VMSelector) updateButtonStates() {
	vm := s.selectedVM()
	hasSelection := vm != nil
	canRun := hasSelection && vm.State != "running"
	objc.Send[objc.ID](s.runButton.ID, objc.Sel("setEnabled:"), canRun)
	objc.Send[objc.ID](s.runButton.ID, objc.Sel("setTitle:"), objc.String(runButtonTitle(vm)))

	// Disable Delete for active VM
	canDelete := hasSelection && vm.Name != s.activeVM && vm.State != "running"
	objc.Send[objc.ID](s.delButton.ID, objc.Sel("setEnabled:"), canDelete)

	canColdBoot := hasSelection && vm.State == "suspended"
	objc.Send[objc.ID](s.coldButton.ID, objc.Sel("setEnabled:"), canColdBoot)
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
	case "state":
		value = selectorStateText(vm)
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
	if vm.State == "running" {
		showSelectorAlert("VM Already Running", fmt.Sprintf("%q is already running.", vm.Name))
		return
	}
	if s.onSelect != nil {
		s.onSelect(*vm, false)
	}
}

// handleColdBoot runs the selected VM after discarding any saved suspend state.
func (s *VMSelector) handleColdBoot(_ objc.ID, _ objc.SEL, _ objc.ID) {
	vm := s.selectedVM()
	if vm == nil || vm.State != "suspended" {
		return
	}
	if s.onSelect != nil {
		s.onSelect(*vm, true)
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
	if vm.State == "running" {
		showSelectorAlert("Cannot Delete Running VM", fmt.Sprintf("Stop %q before deleting it.", vm.Name))
		return
	}
	if vm.Name == s.activeVM {
		showSelectorAlert("Cannot Delete Active VM", fmt.Sprintf("%q is the active VM.", vm.Name))
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
		showSelectorError("Cannot Delete VM", err)
		return
	}

	// Refresh the VM list
	newVMs, err := ListVMs()
	if err != nil {
		showSelectorError("Cannot Refresh VM List", err)
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

func performSelectorAction(action selectorAction) error {
	switch action.kind {
	case selectorActionRun:
		vmDir = action.vm.Path
		vmName = action.vm.Name
		guiMode = true
		skipResume = action.coldBoot
		linuxMode = action.vm.OSType == "Linux"
		if linuxMode {
			return runLinuxVM()
		}
		return runMacOSVM()
	case selectorActionInstall:
		vmName = action.newVM.Name
		vmDir = resolvePath(GetVMPath(action.newVM.Name))
		guiMode = true
		linuxMode = false
		provisionUser = action.newVM.ProvisionUser
		provisionPassword = action.newVM.ProvisionPassword
		provisionAdmin = action.newVM.ProvisionAdmin
		if provisionUser != "" {
			provisionStrategy = "auto"
		}
		err := installMacOSLikeVZ(context.Background())
		if errors.Is(err, errRestartVM) {
			if setErr := SetActiveVM(vmName); setErr != nil {
				return fmt.Errorf("set active VM: %w", setErr)
			}
			return runMacOSVM()
		}
		if err != nil {
			return err
		}
		if setErr := SetActiveVM(vmName); setErr != nil {
			return fmt.Errorf("set active VM: %w", setErr)
		}
		return nil
	default:
		return nil
	}
}

func refreshSelectorVMs() ([]VMInfo, error) {
	vms, err := ListVMs()
	if err != nil {
		return nil, err
	}
	if len(vms) == 0 {
		if info, err := GetVMInfo(vmDir); err == nil {
			vms = append(vms, *info)
		}
	}
	return vms, nil
}

func selectorErrorTitle(action selectorAction) string {
	switch action.kind {
	case selectorActionRun:
		return "Cannot Start VM"
	case selectorActionInstall:
		return "Cannot Create VM"
	default:
		return "Operation Failed"
	}
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

	setupSelectorMenu()

	for {
		var action selectorAction

		// Declare selector before the closures that reference it.
		var selector *VMSelector
		selector = NewVMSelector(vms, func(vm VMInfo, coldBoot bool) {
			action = selectorAction{
				kind:     selectorActionRun,
				vm:       vm,
				coldBoot: coldBoot,
			}
			objc.Send[objc.ID](selector.window.ID, objc.Sel("close"))
			app.Stop(nil)
			postDummyEvent(app)
		}, func() {
			opts, ok := promptForNewVMOptions()
			if !ok {
				return
			}
			action = selectorAction{
				kind:  selectorActionInstall,
				newVM: opts,
			}
			objc.Send[objc.ID](selector.window.ID, objc.Sel("close"))
			app.Stop(nil)
			postDummyEvent(app)
		})

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

		if action.kind == selectorActionNone {
			return
		}
		if err := performSelectorAction(action); err != nil {
			showSelectorError(selectorErrorTitle(action), err)
			refreshed, refreshErr := refreshSelectorVMs()
			if refreshErr != nil {
				showSelectorError("Cannot Refresh VM List", refreshErr)
				return
			}
			vms = refreshed
			continue
		}
		return
	}
}
