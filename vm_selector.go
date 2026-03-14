package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// VM selector window dimensions.
const (
	selectorWindowWidth  = 920
	selectorWindowHeight = 520
	selectorMinWidth     = 760
	selectorMinHeight    = 380
	selectorBarHeight    = 52
	selectorButtonHeight = 30
	selectorDetailWidth  = 290
	selectorViewMinX     = 1
	selectorViewWidth    = 2
	selectorViewHeight   = 16
)

// VMSelector displays a native macOS window with a table of VMs.
type VMSelector struct {
	window        appkit.NSWindow
	tableView     appkit.NSTableView
	runButton     appkit.NSButton
	coldButton    appkit.NSButton
	delButton     appkit.NSButton
	refreshButton appkit.NSButton
	revealButton  appkit.NSButton
	detailTitle   appkit.NSTextField
	detailState   appkit.NSTextField
	detailOS      appkit.NSTextField
	detailSize    appkit.NSTextField
	detailDate    appkit.NSTextField
	detailPath    appkit.NSTextField
	vms           []VMInfo
	activeVM      string
	delegateID    objc.ID
	onSelect      func(VMInfo, bool)
	onInstall     func()
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
	Name               string
	ProvisionUser      string
	ProvisionPassword  string
	ProvisionAdmin     bool
	PostInstallRecipes string
}

const selectorNoRecipe = "(none)"

var vmSelectorDelegateCounter uint64
var selectorScriptRunnerDelegateCounter uint64

type selectorLogWriter struct {
	ch chan string
}

func (w *selectorLogWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.ch <- string(append([]byte(nil), p...))
	return len(p), nil
}

type selectorScriptRunner struct {
	window    appkit.NSWindow
	status    appkit.NSTextField
	popup     appkit.NSPopUpButton
	textView  appkit.NSTextView
	runButton appkit.NSButton
	vm        VMInfo
	delegate  objc.ID
	logCh     chan string
	doneCh    chan error
	running   bool
	closed    bool
}

func newSelectorScriptRunner(vm VMInfo, recipes []string) *selectorScriptRunner {
	r := &selectorScriptRunner{
		vm:     vm,
		logCh:  make(chan string, 1024),
		doneCh: make(chan error, 1),
	}
	r.registerDelegate()
	r.buildWindow(recipes)
	return r
}

func (r *selectorScriptRunner) registerDelegate() {
	className := fmt.Sprintf("VZScriptRunnerDelegate_%d", atomic.AddUint64(&selectorScriptRunnerDelegateCounter, 1))
	cls, err := objc.RegisterClass(
		className,
		objc.GetClass("NSObject"),
		nil, nil,
		[]objc.MethodDef{
			{Cmd: objc.RegisterName("runVZScriptRecipe:"), Fn: r.handleRun},
			{Cmd: objc.RegisterName("closeVZScriptRunner:"), Fn: r.handleClose},
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not register VZScriptRunnerDelegate: %v\n", err)
		return
	}
	r.delegate = objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	r.delegate = objc.Send[objc.ID](r.delegate, objc.Sel("init"))
}

func (r *selectorScriptRunner) buildWindow(recipes []string) {
	frame := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 260, Y: 260},
		Size:   corefoundation.CGSize{Width: 760, Height: 420},
	}
	r.window = appkit.NewWindowWithContentRectStyleMaskBackingDefer(
		frame,
		appkit.NSWindowStyleMaskTitled|
			appkit.NSWindowStyleMaskClosable|
			appkit.NSWindowStyleMaskMiniaturizable,
		appkit.NSBackingStoreBuffered,
		false,
	)
	r.window.SetTitle("Run VZScript")
	r.window.Center()
	objc.Send[objc.ID](r.window.ID, objc.Sel("setReleasedWhenClosed:"), false)

	content := objc.Send[objc.ID](r.window.ID, objc.Sel("contentView"))

	title := appkit.NewTextFieldLabelWithString(fmt.Sprintf("VM: %s", r.vm.Name))
	title.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 16, Y: 390},
		Size:   corefoundation.CGSize{Width: 728, Height: 18},
	})
	objc.Send[objc.ID](content, objc.Sel("addSubview:"), title.ID)

	r.status = appkit.NewTextFieldLabelWithString("Select a recipe and click Run.")
	r.status.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 16, Y: 366},
		Size:   corefoundation.CGSize{Width: 728, Height: 18},
	})
	objc.Send[objc.ID](content, objc.Sel("addSubview:"), r.status.ID)

	r.popup = appkit.NewPopUpButtonWithFramePullsDown(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 16, Y: 338},
		Size:   corefoundation.CGSize{Width: 530, Height: 24},
	}, false)
	if len(recipes) > 0 {
		r.popup.AddItemsWithTitles(recipes)
	}
	objc.Send[objc.ID](content, objc.Sel("addSubview:"), r.popup.ID)

	target := objectivec.ObjectFromID(r.delegate)
	r.runButton = appkit.NewButtonWithTitleTargetAction("Run", target, objc.Sel("runVZScriptRecipe:"))
	r.runButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 560, Y: 336},
		Size:   corefoundation.CGSize{Width: 88, Height: 28},
	})
	objc.Send[objc.ID](r.runButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](content, objc.Sel("addSubview:"), r.runButton.ID)

	closeBtn := appkit.NewButtonWithTitleTargetAction("Close", target, objc.Sel("closeVZScriptRunner:"))
	closeBtn.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 656, Y: 336},
		Size:   corefoundation.CGSize{Width: 88, Height: 28},
	})
	objc.Send[objc.ID](closeBtn.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](content, objc.Sel("addSubview:"), closeBtn.ID)

	scroll := appkit.NewScrollViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 16, Y: 16},
		Size:   corefoundation.CGSize{Width: 728, Height: 312},
	})
	scroll.SetHasVerticalScroller(true)
	scroll.SetHasHorizontalScroller(false)
	scroll.SetAutohidesScrollers(true)
	objc.Send[objc.ID](scroll.ID, objc.Sel("setBorderType:"), appkit.NSBezelBorder)

	r.textView = appkit.NewTextViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   corefoundation.CGSize{Width: 728, Height: 312},
	})
	r.textView.SetEditable(false)
	r.textView.SetSelectable(true)
	r.textView.SetString("")
	mono := appkit.GetNSFontClass().MonospacedSystemFontOfSizeWeight(12, appkit.NSFontWeights.Regular)
	r.textView.SetFont(mono)
	scroll.SetDocumentView(r.textView)
	objc.Send[objc.ID](content, objc.Sel("addSubview:"), scroll.ID)
}

func (r *selectorScriptRunner) runModal() {
	app := getSharedApp()

	nc := foundation.GetNotificationCenterClass().DefaultCenter()
	nsName := objc.String("NSWindowWillCloseNotification")
	objc.Send[objc.ID](nc.ID, objc.Sel("addObserverForName:object:queue:usingBlock:"),
		nsName, r.window.ID, objc.ID(0),
		objc.NewBlock(func(_ objc.Block, _ objc.ID) {
			r.closed = true
			app.StopModal()
		}),
	)

	r.window.MakeKeyAndOrderFront(nil)
	app.Activate()

	var logText strings.Builder
	var scheduleTimer func()
	scheduleTimer = func() {
		foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(0.05, false, func(_ *foundation.NSTimer) {
			updated := false
			for i := 0; i < 256; i++ {
				select {
				case chunk := <-r.logCh:
					logText.WriteString(chunk)
					updated = true
				default:
					i = 256
				}
			}
			if updated {
				text := logText.String()
				r.textView.SetString(text)
				r.textView.ScrollRangeToVisible(foundation.NSRange{
					Location: uint(len(text)),
					Length:   0,
				})
			}

			if r.running {
				select {
				case err := <-r.doneCh:
					r.running = false
					objc.Send[objc.ID](r.runButton.ID, objc.Sel("setEnabled:"), true)
					objc.Send[objc.ID](r.popup.ID, objc.Sel("setEnabled:"), true)
					if err != nil {
						r.status.SetStringValue("Failed: " + err.Error())
					} else {
						r.status.SetStringValue("Complete.")
					}
				default:
				}
			}

			if !r.closed {
				scheduleTimer()
			}
		})
	}
	scheduleTimer()
	app.RunModalForWindow(r.window)
}

func (r *selectorScriptRunner) handleRun(_ objc.ID, _ objc.SEL, _ objc.ID) {
	if r.running {
		return
	}
	recipe := strings.TrimSpace(r.popup.TitleOfSelectedItem())
	if recipe == "" {
		r.status.SetStringValue("Select a recipe first.")
		return
	}
	r.status.SetStringValue("Running " + recipe + "...")
	r.running = true
	objc.Send[objc.ID](r.runButton.ID, objc.Sel("setEnabled:"), false)
	objc.Send[objc.ID](r.popup.ID, objc.Sel("setEnabled:"), false)
	go func() {
		r.doneCh <- runVZScriptRecipeOnRunningVM(r.vm, recipe, &selectorLogWriter{ch: r.logCh})
	}()
}

func (r *selectorScriptRunner) handleClose(_ objc.ID, _ objc.SEL, _ objc.ID) {
	r.closed = true
	getSharedApp().StopModal()
	r.window.Close()
}

func runVZScriptRecipeOnRunningVM(vm VMInfo, recipe string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	sock := filepath.Join(vm.Path, "control.sock")
	if !isVMRunning(sock) {
		return fmt.Errorf("vm %q is not running; start it and retry", vm.Name)
	}
	cfg := vzscriptConfig{
		socketPath:  sock,
		execTimeout: 30 * time.Minute,
		verbose:     true,
		logWriter:   out,
		streamOut:   out,
		streamErr:   out,
	}
	fmt.Fprintf(out, "\n=== Running vzscript on %s: %s ===\n", vm.Name, recipe)
	if err := runVZScriptWithDeps([]string{recipe}, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "=== Done: %s ===\n", recipe)
	return nil
}

func runPostInstallVZScriptsWithSelectorUI(recipes string) error {
	app := getSharedApp()

	contentRect := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 240, Y: 240},
		Size:   corefoundation.CGSize{Width: 840, Height: 520},
	}
	window := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
		contentRect,
		appkit.NSWindowStyleMaskTitled,
		appkit.NSBackingStoreBuffered,
		false,
	)
	window.SetTitle("Post-install VZScript Output")
	window.Center()
	objc.Send[objc.ID](window.ID, objc.Sel("setReleasedWhenClosed:"), false)

	contentViewID := objc.Send[objc.ID](window.ID, objc.Sel("contentView"))

	status := appkit.NewTextFieldLabelWithString("Running post-install scripts. VM window and log window are both active.")
	status.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 16, Y: 486},
		Size:   corefoundation.CGSize{Width: 808, Height: 20},
	})
	objc.Send[objc.ID](contentViewID, objc.Sel("addSubview:"), status.ID)

	scroll := appkit.NewScrollViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 16, Y: 16},
		Size:   corefoundation.CGSize{Width: 808, Height: 460},
	})
	scroll.SetHasVerticalScroller(true)
	scroll.SetHasHorizontalScroller(false)
	scroll.SetAutohidesScrollers(true)
	objc.Send[objc.ID](scroll.ID, objc.Sel("setBorderType:"), appkit.NSBezelBorder)

	textView := appkit.NewTextViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   corefoundation.CGSize{Width: 808, Height: 460},
	})
	textView.SetEditable(false)
	textView.SetSelectable(true)
	textView.SetString("")
	mono := appkit.GetNSFontClass().MonospacedSystemFontOfSizeWeight(12, appkit.NSFontWeights.Regular)
	textView.SetFont(mono)
	scroll.SetDocumentView(textView)
	objc.Send[objc.ID](contentViewID, objc.Sel("addSubview:"), scroll.ID)

	logCh := make(chan string, 2048)
	doneCh := make(chan error, 1)
	go func() {
		defer close(logCh)
		doneCh <- runPostInstallVZScriptsWithSelectorOutput(recipes, &selectorLogWriter{ch: logCh})
		close(doneCh)
	}()

	window.MakeKeyAndOrderFront(nil)
	app.Activate()

	var logText strings.Builder
	var runErr error
	done := false
	logClosed := false

	var scheduleTimer func()
	scheduleTimer = func() {
		foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(
			0.05,
			false,
			func(_ *foundation.NSTimer) {
				updated := false
				for i := 0; i < 512; i++ {
					select {
					case chunk, ok := <-logCh:
						if !ok {
							logClosed = true
							i = 512
							continue
						}
						logText.WriteString(chunk)
						updated = true
					default:
						i = 512
					}
				}

				if !done {
					select {
					case runErr = <-doneCh:
						done = true
						if runErr != nil {
							status.SetStringValue("Post-install scripts failed.")
						} else {
							status.SetStringValue("Post-install scripts complete.")
						}
					default:
					}
				}

				if updated {
					text := logText.String()
					textView.SetString(text)
					textView.ScrollRangeToVisible(foundation.NSRange{
						Location: uint(len(text)),
						Length:   0,
					})
				}

				if done && logClosed {
					app.StopModal()
					return
				}

				scheduleTimer()
			},
		)
	}

	scheduleTimer()
	app.RunModalForWindow(window)
	window.Close()
	return runErr
}

func runPostInstallVZScriptsWithSelectorOutput(recipes string, out io.Writer) (retErr error) {
	if out == nil {
		out = os.Stdout
	}

	names := splitRecipes(recipes)
	if len(names) == 0 {
		return nil
	}

	for _, name := range names {
		if _, err := loadVZScriptData(name); err != nil {
			return fmt.Errorf("recipe %q: %w", name, err)
		}
	}

	fmt.Fprintf(out, "\n=== Post-install: running %d vzscript(s) ===\n", len(names))
	for _, n := range names {
		fmt.Fprintf(out, "  - %s\n", n)
	}
	fmt.Fprintln(out)

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	vmCmd := exec.Command(exePath, "run", "-vm", vmName, "-gui=true")
	vmCmd.Stdout = out
	vmCmd.Stderr = out
	if err := vmCmd.Start(); err != nil {
		return fmt.Errorf("start vm gui process: %w", err)
	}

	vmErr := make(chan error, 1)
	go func() {
		vmErr <- vmCmd.Wait()
	}()

	defer func() {
		if retErr == nil {
			return
		}
		if vmCmd.Process != nil {
			_ = vmCmd.Process.Kill()
		}
		select {
		case <-vmErr:
		case <-time.After(5 * time.Second):
		}
	}()

	cfg := vzscriptConfig{
		socketPath:  GetControlSocketPath(),
		execTimeout: 30 * time.Minute,
		verbose:     true,
		logWriter:   out,
		streamOut:   out,
		streamErr:   out,
	}

	fmt.Fprintln(out, "Waiting for VM to boot and guest agent...")
	waitScript := []byte("guest-wait 15m\n")
	if err := runVZScriptOrVMErr(waitScript, "wait-for-agent", cfg, vmErr); err != nil {
		return fmt.Errorf("waiting for agent: %w", err)
	}

	for _, name := range names {
		fmt.Fprintf(out, "\n=== Running vzscript: %s ===\n", name)
		data, err := loadVZScriptData(name)
		if err != nil {
			return err
		}
		if err := runVZScriptOrVMErr(data, name, cfg, vmErr); err != nil {
			return fmt.Errorf("vzscript %s: %w", name, err)
		}
		fmt.Fprintf(out, "=== Done: %s ===\n", name)
	}

	fmt.Fprintln(out, "\nShutting down VM...")
	_, _ = ctlSendRequest(cfg.socketPath, &controlpb.ControlRequest{Type: "agent-shutdown"}, 30*time.Second, "agent-shutdown")

	select {
	case err := <-vmErr:
		if err != nil {
			fmt.Fprintf(out, "VM exited with error: %v\n", err)
		}
	case <-time.After(2 * time.Minute):
		fmt.Fprintln(out, "VM did not exit within timeout.")
		if vmCmd.Process != nil {
			_ = vmCmd.Process.Kill()
		}
	}

	fmt.Fprintln(out, "\nPost-install complete.")
	return nil
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
	} else {
		if err := validateUsername(opts.ProvisionUser); err != nil {
			return err
		}
		if opts.ProvisionPassword == "" {
			return errors.New("enter a password for provisioning")
		}
	}
	for _, name := range splitRecipes(opts.PostInstallRecipes) {
		if _, err := loadVZScriptData(name); err != nil {
			return fmt.Errorf("invalid post-install recipe %q: %w", name, err)
		}
	}
	return nil
}

func listBuiltinVZScriptNames() ([]string, error) {
	entries, err := fs.ReadDir(builtinScripts, "vzscripts")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".vzscript") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".vzscript"))
	}
	sort.Strings(names)
	return names, nil
}

func normalizeRecipeSelection(primary string) string {
	primary = strings.TrimSpace(primary)
	if primary == "" || primary == selectorNoRecipe {
		return ""
	}
	return primary
}

func newVMAccessoryView(opts newVMOptions, recipeNames []string) (appkit.NSView, appkit.NSTextField, appkit.NSTextField, appkit.NSSecureTextField, appkit.NSPopUpButton) {
	view := appkit.NewViewWithFrame(corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: 320, Height: 144},
	})

	addLabel := func(text string, y float64) {
		label := appkit.NewTextFieldLabelWithString(text)
		label.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 0, Y: y},
			Size:   corefoundation.CGSize{Width: 92, Height: 22},
		})
		objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), label.ID)
	}

	addLabel("VM Name:", 112)
	addLabel("Username:", 78)
	addLabel("Password:", 44)
	addLabel("Advanced:", 10)

	nameField := appkit.NewTextFieldWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 96, Y: 108},
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
		Origin: corefoundation.CGPoint{X: 96, Y: 74},
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
		Origin: corefoundation.CGPoint{X: 96, Y: 40},
		Size:   corefoundation.CGSize{Width: 224, Height: 24},
	})
	passwordField.SetStringValue(opts.ProvisionPassword)
	passwordField.SetPlaceholderString("optional")
	passwordField.SetEditable(true)
	passwordField.SetSelectable(true)
	passwordField.SetAccessibilityLabel("Password")
	passwordField.SetAccessibilityIdentifier("new-vm-password")
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), passwordField.ID)

	recipePopup := appkit.NewPopUpButtonWithFramePullsDown(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 96, Y: 6},
		Size:   corefoundation.CGSize{Width: 224, Height: 24},
	}, false)
	recipePopup.AddItemWithTitle(selectorNoRecipe)
	if len(recipeNames) > 0 {
		recipePopup.AddItemsWithTitles(recipeNames)
	}

	selectedRecipe := selectorNoRecipe
	known := make(map[string]bool, len(recipeNames))
	for _, n := range recipeNames {
		known[n] = true
	}
	for _, r := range splitRecipes(opts.PostInstallRecipes) {
		if selectedRecipe == selectorNoRecipe && known[r] {
			selectedRecipe = r
		}
	}
	recipePopup.SelectItemWithTitle(selectedRecipe)
	recipePopup.SetAccessibilityLabel("Advanced Post-install Script")
	recipePopup.SetAccessibilityIdentifier("new-vm-recipe")
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), recipePopup.ID)

	return view, nameField, userField, passwordField, recipePopup
}

func promptForNewVMOptions() (newVMOptions, bool) {
	baseDir := GetVMBaseDir()
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		showSelectorAlert("Cannot Create VM", fmt.Sprintf("Could not create %s: %v", baseDir, err))
		return newVMOptions{}, false
	}
	baseDir = resolvePath(baseDir)
	opts := newVMOptions{
		Name:               nextNewVMName(),
		ProvisionUser:      strings.TrimSpace(provisionUser),
		ProvisionPassword:  provisionPassword,
		ProvisionAdmin:     true,
		PostInstallRecipes: strings.TrimSpace(installVZScripts),
	}
	recipeNames, _ := listBuiltinVZScriptNames()
	for {
		alert := appkit.NewNSAlert()
		alert.SetMessageText("Create New VM")
		alert.SetInformativeText(fmt.Sprintf("Create in %s.\nOptional username/password creates an admin user.\nAdvanced script runs after first boot.", baseDir))
		alert.AddButtonWithTitle("Create")
		alert.AddButtonWithTitle("Cancel")
		accessoryView, nameField, userField, passwordField, recipePopup := newVMAccessoryView(opts, recipeNames)
		alert.SetAccessoryView(accessoryView)

		if alert.RunModal() != appkit.AlertFirstButtonReturn {
			return newVMOptions{}, false
		}

		opts.Name = strings.TrimSpace(nameField.StringValue())
		opts.ProvisionUser = strings.TrimSpace(userField.StringValue())
		opts.ProvisionPassword = passwordField.StringValue()
		opts.ProvisionAdmin = true
		opts.PostInstallRecipes = normalizeRecipeSelection(recipePopup.TitleOfSelectedItem())

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

func runningVMNames(excludeName string) []string {
	vms, err := ListVMs()
	if err != nil {
		return nil
	}
	var names []string
	for _, vm := range vms {
		if vm.State == "running" && vm.Name != excludeName {
			names = append(names, vm.Name)
		}
	}
	sort.Strings(names)
	return names
}

func isVMLimitError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "maximum supported number of active virtual machines") {
		return true
	}
	if strings.Contains(lower, "number of virtual machines exceeds the limit") {
		return true
	}

	var snap nsErrorSnapshot
	if errors.As(err, &snap) && strings.EqualFold(snap.domain, "VZErrorDomain") && snap.code == 6 {
		return true
	}
	return false
}

func withVMLimitHint(err error, targetVM string) error {
	if err == nil {
		return nil
	}
	running := runningVMNames(targetVM)
	lower := strings.ToLower(err.Error())
	genericStartFailure := strings.Contains(lower, "virtual machine failed to start")
	if !isVMLimitError(err) && !(genericStartFailure && len(running) > 0) {
		return err
	}

	if len(running) == 0 {
		return fmt.Errorf("host virtualization limit reached; stop another running VM and retry: %w", err)
	}
	return fmt.Errorf("host virtualization limit reached (%d running: %s); stop one and retry: %w",
		len(running), strings.Join(running, ", "), err)
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
	className := fmt.Sprintf("VMSelectorDelegate_%d", atomic.AddUint64(&vmSelectorDelegateCounter, 1))
	cls, err := objc.RegisterClass(
		className,
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
			{Cmd: objc.RegisterName("refreshVMs:"), Fn: s.handleRefresh},
			{Cmd: objc.RegisterName("revealVMInFinder:"), Fn: s.handleRevealInFinder},
			{Cmd: objc.RegisterName("openVZScriptRunner:"), Fn: s.handleOpenVZScriptRunner},
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

	tableWidth := float64(selectorWindowWidth - selectorDetailWidth)

	// Build the table view
	tableFrame := corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: tableWidth, Height: selectorWindowHeight - selectorBarHeight},
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
	s.addColumn("name", "Name", 220, true)
	s.addColumn("state", "State", 90, false)
	s.addColumn("os", "OS", 80, false)
	s.addColumn("size", "Size", 90, false)
	s.addColumn("created", "Created", 100, false)

	// Wrap table in scroll view
	scrollView := appkit.NewScrollViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: selectorBarHeight},
		Size:   corefoundation.CGSize{Width: tableWidth, Height: selectorWindowHeight - selectorBarHeight},
	})
	objc.Send[objc.ID](scrollView.ID, objc.Sel("retain"))
	objc.Send[objc.ID](scrollView.ID, objc.Sel("setDocumentView:"), s.tableView.ID)
	objc.Send[objc.ID](scrollView.ID, objc.Sel("setHasVerticalScroller:"), true)
	// Keep the table pinned left and sized to fill available width.
	objc.Send[objc.ID](scrollView.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewHeight))

	details := s.buildDetailsPanel(tableWidth)

	// Build button bar
	buttonBar := s.buildButtonBar()

	// Add subviews to the window's content view
	contentViewID := objc.Send[objc.ID](s.window.ID, objc.Sel("contentView"))
	objc.Send[objc.ID](contentViewID, objc.Sel("addSubview:"), scrollView.ID)
	objc.Send[objc.ID](contentViewID, objc.Sel("addSubview:"), details.ID)
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

func (s *VMSelector) buildDetailsPanel(x float64) appkit.NSView {
	panel := appkit.NewViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: x, Y: selectorBarHeight},
		Size:   corefoundation.CGSize{Width: selectorDetailWidth, Height: selectorWindowHeight - selectorBarHeight},
	})
	objc.Send[objc.ID](panel.ID, objc.Sel("retain"))
	// Keep the details panel pinned to the right edge.
	objc.Send[objc.ID](panel.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX|selectorViewHeight))

	makeLabel := func(text string, y float64) appkit.NSTextField {
		label := appkit.NewTextFieldLabelWithString(text)
		label.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 14, Y: y},
			Size:   corefoundation.CGSize{Width: selectorDetailWidth - 28, Height: 22},
		})
		objc.Send[objc.ID](panel.ID, objc.Sel("addSubview:"), label.ID)
		return label
	}

	s.detailTitle = makeLabel("VM Details", selectorWindowHeight-selectorBarHeight-38)
	s.detailState = makeLabel("Status:", selectorWindowHeight-selectorBarHeight-72)
	s.detailOS = makeLabel("OS:", selectorWindowHeight-selectorBarHeight-96)
	s.detailSize = makeLabel("Disk:", selectorWindowHeight-selectorBarHeight-120)
	s.detailDate = makeLabel("Created:", selectorWindowHeight-selectorBarHeight-144)
	makeLabel("Path:", selectorWindowHeight-selectorBarHeight-174)
	s.detailPath = makeLabel("", selectorWindowHeight-selectorBarHeight-198)
	s.detailPath.SetUsesSingleLineMode(true)
	s.detailPath.SetLineBreakMode(appkit.NSLineBreakByTruncatingMiddle)

	s.revealButton = appkit.NewButtonWithTitleTargetAction("Reveal in Finder", objectivec.ObjectFromID(s.delegateID), objc.Sel("revealVMInFinder:"))
	s.revealButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 14, Y: 14},
		Size:   corefoundation.CGSize{Width: 132, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.revealButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](s.revealButton.ID, objc.Sel("setEnabled:"), false)
	objc.Send[objc.ID](panel.ID, objc.Sel("addSubview:"), s.revealButton.ID)

	return panel
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
	objc.Send[objc.ID](bar.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth))

	target := objectivec.ObjectFromID(s.delegateID)

	// "+ New VM..." button (left-aligned)
	newBtn := appkit.NewButtonWithTitleTargetAction("+ New VM...", target, objc.Sel("createVM:"))
	newBtn.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 12, Y: 10},
		Size:   corefoundation.CGSize{Width: 100, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](newBtn.ID, objc.Sel("setBezelStyle:"), int(1)) // NSBezelStyleRounded
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), newBtn.ID)

	// "Refresh" button (left-aligned, after New VM)
	s.refreshButton = appkit.NewButtonWithTitleTargetAction("Refresh", target, objc.Sel("refreshVMs:"))
	s.refreshButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 12 + 100 + 8, Y: 10},
		Size:   corefoundation.CGSize{Width: 82, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.refreshButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), s.refreshButton.ID)

	// "Run" button (right-aligned, default button)
	s.runButton = appkit.NewButtonWithTitleTargetAction("Run", target, objc.Sel("runVM:"))
	s.runButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: selectorWindowWidth - 12 - 80, Y: 10},
		Size:   corefoundation.CGSize{Width: 80, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.runButton.ID, objc.Sel("setBezelStyle:"), int(1))
	// Keep right-side buttons anchored to the trailing edge.
	objc.Send[objc.ID](s.runButton.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX))
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), s.runButton.ID)

	// "Cold Boot" button (right-aligned, before Run)
	s.coldButton = appkit.NewButtonWithTitleTargetAction("Cold Boot", target, objc.Sel("coldBootVM:"))
	s.coldButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: selectorWindowWidth - 12 - 80 - 8 - 100, Y: 10},
		Size:   corefoundation.CGSize{Width: 100, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.coldButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](s.coldButton.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX))
	objc.Send[objc.ID](bar.ID, objc.Sel("addSubview:"), s.coldButton.ID)

	// "Delete" button (right-aligned, before Run)
	s.delButton = appkit.NewButtonWithTitleTargetAction("Delete", target, objc.Sel("deleteVM:"))
	s.delButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: selectorWindowWidth - 12 - 80 - 8 - 100 - 8 - 80, Y: 10},
		Size:   corefoundation.CGSize{Width: 80, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.delButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](s.delButton.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX))
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
	objc.Send[objc.ID](s.revealButton.ID, objc.Sel("setEnabled:"), hasSelection)
	s.updateDetailsPanel(vm)
}

func (s *VMSelector) updateDetailsPanel(vm *VMInfo) {
	if vm == nil {
		s.detailTitle.SetStringValue("No VM Selected")
		s.detailState.SetStringValue("Status: -")
		s.detailOS.SetStringValue("OS: -")
		s.detailSize.SetStringValue("Disk: -")
		s.detailDate.SetStringValue("Created: -")
		s.detailPath.SetStringValue("")
		return
	}

	title := vm.Name
	if vm.Name == s.activeVM {
		title += " (active)"
	}
	s.detailTitle.SetStringValue(title)
	s.detailState.SetStringValue("Status: " + selectorStateText(*vm))
	s.detailOS.SetStringValue("OS: " + vm.OSType)
	s.detailSize.SetStringValue("Disk: " + FormatSize(vm.DiskSize))
	s.detailDate.SetStringValue("Created: " + vm.Created.Format("2006-01-02 15:04"))
	s.detailPath.SetStringValue(vm.Path)
}

func (s *VMSelector) selectRowByName(name string) bool {
	if name == "" {
		return false
	}
	for i := range s.vms {
		if s.vms[i].Name == name {
			indexSet := objc.Send[objc.ID](
				objc.Send[objc.ID](objc.ID(objc.GetClass("NSIndexSet")), objc.Sel("alloc")),
				objc.Sel("initWithIndex:"), uint(i),
			)
			objc.Send[objc.ID](s.tableView.ID, objc.Sel("selectRowIndexes:byExtendingSelection:"), indexSet, false)
			return true
		}
	}
	return false
}

func (s *VMSelector) reloadVMList() error {
	selected := s.selectedVM()
	selectedName := ""
	if selected != nil {
		selectedName = selected.Name
	}

	vms, err := ListVMs()
	if err != nil {
		return err
	}
	s.vms = vms
	s.activeVM = GetActiveVM()
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("reloadData"))

	if !s.selectRowByName(selectedName) && !s.selectRowByName(s.activeVM) {
		if row := s.initialSelectionRow(); row >= 0 {
			indexSet := objc.Send[objc.ID](
				objc.Send[objc.ID](objc.ID(objc.GetClass("NSIndexSet")), objc.Sel("alloc")),
				objc.Sel("initWithIndex:"), uint(row),
			)
			objc.Send[objc.ID](s.tableView.ID, objc.Sel("selectRowIndexes:byExtendingSelection:"), indexSet, false)
		}
	}
	s.updateButtonStates()
	return nil
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

// handleRefresh reloads VM metadata and updates selection.
func (s *VMSelector) handleRefresh(_ objc.ID, _ objc.SEL, _ objc.ID) {
	if err := s.reloadVMList(); err != nil {
		showSelectorError("Cannot Refresh VM List", err)
	}
}

// handleRevealInFinder reveals the selected VM in Finder.
func (s *VMSelector) handleRevealInFinder(_ objc.ID, _ objc.SEL, _ objc.ID) {
	vm := s.selectedVM()
	if vm == nil {
		return
	}
	if err := exec.Command("open", "-R", vm.Path).Run(); err != nil {
		showSelectorError("Cannot Reveal VM", fmt.Errorf("open finder: %w", err))
	}
}

func (s *VMSelector) handleOpenVZScriptRunner(_ objc.ID, _ objc.SEL, _ objc.ID) {
	vm := s.selectedVM()
	if vm == nil {
		showSelectorAlert("No VM Selected", "Select a running VM first.")
		return
	}
	if vm.State != "running" {
		showSelectorAlert("VM Not Running", fmt.Sprintf("Start %q first, then run vzscripts.", vm.Name))
		return
	}
	recipes, err := listBuiltinVZScriptNames()
	if err != nil {
		showSelectorError("Cannot Load VZScripts", err)
		return
	}
	if len(recipes) == 0 {
		showSelectorAlert("No VZScripts Found", "No built-in recipes are available.")
		return
	}
	runner := newSelectorScriptRunner(*vm, recipes)
	runner.runModal()
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

	if err := s.reloadVMList(); err != nil {
		showSelectorError("Cannot Refresh VM List", err)
	}
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
			return withVMLimitHint(runLinuxVM(), vmName)
		}
		return withVMLimitHint(runMacOSVM(), vmName)
	case selectorActionInstall:
		vmName = action.newVM.Name
		vmDir = resolvePath(GetVMPath(action.newVM.Name))
		guiMode = true
		linuxMode = false
		postInstallRecipes := strings.TrimSpace(action.newVM.PostInstallRecipes)
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
			if postInstallRecipes != "" {
				return withVMLimitHint(runPostInstallVZScriptsWithSelectorUI(postInstallRecipes), vmName)
			}
			return withVMLimitHint(runMacOSVM(), vmName)
		}
		if err != nil {
			return withVMLimitHint(err, vmName)
		}
		if setErr := SetActiveVM(vmName); setErr != nil {
			return fmt.Errorf("set active VM: %w", setErr)
		}
		if postInstallRecipes != "" {
			return withVMLimitHint(runPostInstallVZScriptsWithSelectorUI(postInstallRecipes), vmName)
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
		setupSelectorMenu(selector.delegateID)

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
