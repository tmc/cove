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

	"github.com/tmc/vz-macos/internal/bytefmt"
	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"

	"golang.org/x/tools/txtar"
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
	selectorContentInset = 18
	selectorPanelGap     = 18
	selectorHeaderHeight = 58
	selectorSectionGap   = 12
	selectorRowHeight    = 60
	selectorViewMinX     = 1
	selectorViewMinY     = 4
	selectorViewWidth    = 2
	selectorViewHeight   = 16
)

// VMSelector displays a native macOS window with a table of VMs.
type VMSelector struct {
	window        appkit.NSWindow
	listScroll    appkit.NSScrollView
	tableView     appkit.NSTableView
	runButton     appkit.NSButton
	coldButton    appkit.NSButton
	delButton     appkit.NSButton
	refreshButton appkit.NSButton
	revealButton  appkit.NSButton
	scriptButton  appkit.NSButton
	countLabel    appkit.NSTextField
	emptyState    appkit.NSView
	detailTitle   appkit.NSTextField
	detailState   appkit.NSTextField
	detailOS      appkit.NSTextField
	detailSize    appkit.NSTextField
	detailDate    appkit.NSTextField
	detailPath    appkit.NSTextField
	selectedRow   int
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

// defaultRecipePreset lists the recipes selected by the "Use defaults" checkbox.
var defaultRecipePreset = []string{"homebrew", "golang", "developer-tools"}

// recipeSelectorMeta lists scripts that are not useful as post-install selections.
var recipeSelectorHidden = map[string]bool{
	"setup-assistant": true,
	"setup-user":      true,
	"login":           true,
	"full-setup":      true,
	"deploy-agent":    true,
}

var vmSelectorDelegateCounter uint64
var selectorScriptRunnerDelegateCounter uint64

func selectorMonospacedRegularWeight() appkit.NSFontWeight {
	switch v := any(appkit.NSFontWeights.Regular).(type) {
	case appkit.NSFontWeight:
		return v
	case func() appkit.NSFontWeight:
		return v()
	default:
		return appkit.NSFontWeight(0)
	}
}

func selectorVMCountText(count int) string {
	suffix := "s"
	if count == 1 {
		suffix = ""
	}
	return fmt.Sprintf("%d VM%s", count, suffix)
}

func selectorRowTitle(vm VMInfo, activeVM string) string {
	title := vm.Name
	if vm.Name == activeVM {
		title += " *"
	}
	return title
}

func selectorRowSubtitle(vm VMInfo) string {
	return fmt.Sprintf("%s | %s | %s", vm.OSType, bytefmt.Size(vm.DiskSize), vm.Created.Format("2006-01-02"))
}

func selectorLabel(
	text string,
	frame corefoundation.CGRect,
	font appkit.NSFont,
	color appkit.INSColor,
) appkit.NSTextField {
	label := appkit.NewTextFieldLabelWithString(text)
	label.SetFrame(frame)
	label.SetFont(font)
	label.SetTextColor(color)
	label.SetDrawsBackground(false)
	label.SetBezeled(false)
	label.SetBordered(false)
	label.SetEditable(false)
	label.SetSelectable(false)
	return label
}

func selectorPanel(frame corefoundation.CGRect) appkit.NSBox {
	panel := appkit.NewBoxWithFrame(frame)
	panel.SetBoxType(nsBoxCustom)
	objc.Send[struct{}](panel.ID, objc.Sel("setBorderType:"), appkit.NSLineBorder)
	panel.SetTitlePosition(appkit.NSNoTitle)
	panel.SetBorderWidth(0.75)
	panel.SetCornerRadius(16)
	panel.SetBorderColor(appkit.GetNSColorClass().SeparatorColor().ColorWithAlphaComponent(0.12))
	panel.SetFillColor(appkit.GetNSColorClass().ControlBackgroundColor().ColorWithAlphaComponent(0.56))
	return panel
}

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
	objc.Send[objc.ID](r.textView.ID, objc.Sel("setString:"), foundation.NewStringWithString(""))
	mono := appkit.GetNSFontClass().MonospacedSystemFontOfSizeWeight(12, selectorMonospacedRegularWeight())
	r.textView.SetFont(mono)
	scroll.SetDocumentView(r.textView.NSView)
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
				objc.Send[objc.ID](r.textView.ID, objc.Sel("setString:"), foundation.NewStringWithString(text))
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
		appkit.NSWindowStyleMaskTitled|appkit.NSWindowStyleMaskClosable|appkit.NSWindowStyleMaskMiniaturizable|appkit.NSWindowStyleMaskResizable,
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
	objc.Send[objc.ID](textView.ID, objc.Sel("setString:"), foundation.NewStringWithString(""))
	mono := appkit.GetNSFontClass().MonospacedSystemFontOfSizeWeight(12, selectorMonospacedRegularWeight())
	textView.SetFont(mono)
	scroll.SetDocumentView(textView.NSView)
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
					objc.Send[objc.ID](textView.ID, objc.Sel("setString:"), foundation.NewStringWithString(text))
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
	alert.SetAlertStyle(nsAlertStyleWarning)
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

// recipeInfo holds parsed metadata for a vzscript recipe.
type recipeInfo struct {
	name     string
	desc     string
	requires []string
}

// recipeCheckboxRow pairs a recipe with its UI controls.
type recipeCheckboxRow struct {
	info     recipeInfo
	checkbox appkit.NSButton
}

// recipeSelector manages the multi-select recipe UI in the New VM dialog.
type recipeSelector struct {
	rows          []recipeCheckboxRow
	byName        map[string]*recipeCheckboxRow
	defaultsCheck appkit.NSButton
	disclosureBtn appkit.NSButton
	container     appkit.NSView
	recipeSection appkit.NSView
	topSubviews   []appkit.NSView
	expanded      bool
	alert         appkit.NSAlert
}

// bindAlert stores the alert reference and wires the disclosure toggle.
func (s *recipeSelector) bindAlert(alert appkit.NSAlert) {
	s.alert = alert
	s.disclosureBtn.SetActionHandler(func() {
		s.toggleDisclosure()
	})
}

const recipeSectionHeight = 200.0

// toggleDisclosure expands or collapses the recipe section.
func (s *recipeSelector) toggleDisclosure() {
	s.expanded = !s.expanded
	if s.expanded {
		for _, sv := range s.topSubviews {
			f := sv.Frame()
			f.Origin.Y += recipeSectionHeight
			sv.SetFrame(f)
		}
		f := s.container.Frame()
		f.Size.Height += recipeSectionHeight
		s.container.SetFrame(f)
		s.recipeSection.SetHidden(false)
	} else {
		for _, sv := range s.topSubviews {
			f := sv.Frame()
			f.Origin.Y -= recipeSectionHeight
			sv.SetFrame(f)
		}
		f := s.container.Frame()
		f.Size.Height -= recipeSectionHeight
		s.container.SetFrame(f)
		s.recipeSection.SetHidden(true)
	}
	if s.alert.ID != 0 {
		s.alert.Layout()
	}
}

// selectedRecipes returns a comma-separated list of checked recipe names.
func (s *recipeSelector) selectedRecipes() string {
	var selected []string
	for _, row := range s.rows {
		if row.checkbox.State() == appkit.NSControlStateValue(1) {
			selected = append(selected, row.info.name)
		}
	}
	return strings.Join(selected, ",")
}

// updateDefaultsState syncs the "Use defaults" checkbox with individual recipe state.
func (s *recipeSelector) updateDefaultsState() {
	allOn := true
	for _, name := range defaultRecipePreset {
		if row, ok := s.byName[name]; ok {
			if row.checkbox.State() != appkit.NSControlStateValue(1) {
				allOn = false
				break
			}
		} else {
			allOn = false
			break
		}
	}
	if allOn {
		s.defaultsCheck.SetState(appkit.NSControlStateValue(1))
	} else {
		s.defaultsCheck.SetState(appkit.NSControlStateValue(0))
	}
}

// autoCheckDeps recursively checks transitive dependencies for a recipe.
func (s *recipeSelector) autoCheckDeps(name string, visited map[string]bool) {
	if visited[name] {
		return
	}
	visited[name] = true
	row, ok := s.byName[name]
	if !ok {
		return
	}
	for _, dep := range row.info.requires {
		if r, ok := s.byName[dep]; ok {
			r.checkbox.SetState(appkit.NSControlStateValue(1))
		}
		s.autoCheckDeps(dep, visited)
	}
}

// loadRecipeInfos returns metadata for all user-selectable builtin recipes.
func loadRecipeInfos() []recipeInfo {
	names, err := listBuiltinVZScriptNames()
	if err != nil {
		return nil
	}
	var recipes []recipeInfo
	for _, name := range names {
		if recipeSelectorHidden[name] {
			continue
		}
		data, err := loadVZScriptData(name)
		if err != nil {
			continue
		}
		ar := txtar.Parse(data)
		meta := parseScriptMeta(ar.Comment)
		recipes = append(recipes, recipeInfo{
			name:     name,
			desc:     meta.desc,
			requires: meta.requires,
		})
	}
	return recipes
}

func newVMAccessoryView(opts newVMOptions, recipes []recipeInfo) (appkit.NSView, appkit.NSTextField, appkit.NSTextField, appkit.NSSecureTextField, *recipeSelector) {
	const (
		viewWidth       = 320.0
		labelWidth      = 92.0
		fieldX          = 96.0
		fieldWidth      = 224.0
		fieldHeight     = 24.0
		collapsedHeight = 144.0
		checkboxRowH    = 22.0
		depLabelH       = 14.0
		recipeRowH      = checkboxRowH + depLabelH + 2
	)

	view := appkit.NewViewWithFrame(corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: viewWidth, Height: collapsedHeight},
	})

	sel := &recipeSelector{byName: map[string]*recipeCheckboxRow{}, container: view}

	// Helper to create a label and track it for disclosure repositioning.
	addLabel := func(text string, y float64) appkit.NSTextField {
		label := appkit.NewTextFieldLabelWithString(text)
		label.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 0, Y: y},
			Size:   corefoundation.CGSize{Width: labelWidth, Height: 22},
		})
		objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), label.ID)
		return label
	}

	lbl1 := addLabel("VM Name:", 112)
	lbl2 := addLabel("Username:", 78)
	lbl3 := addLabel("Password:", 44)

	nameField := appkit.NewTextFieldWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: fieldX, Y: 108},
		Size:   corefoundation.CGSize{Width: fieldWidth, Height: fieldHeight},
	})
	nameField.SetStringValue(opts.Name)
	nameField.SetPlaceholderString("macos")
	nameField.SetEditable(true)
	nameField.SetSelectable(true)
	nameField.SetAccessibilityLabel("VM Name")
	nameField.SetAccessibilityIdentifier("new-vm-name")
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), nameField.ID)

	userField := appkit.NewTextFieldWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: fieldX, Y: 74},
		Size:   corefoundation.CGSize{Width: fieldWidth, Height: fieldHeight},
	})
	userField.SetStringValue(opts.ProvisionUser)
	userField.SetPlaceholderString("optional")
	userField.SetEditable(true)
	userField.SetSelectable(true)
	userField.SetAccessibilityLabel("Username")
	userField.SetAccessibilityIdentifier("new-vm-username")
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), userField.ID)

	passwordField := appkit.NewSecureTextFieldWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: fieldX, Y: 40},
		Size:   corefoundation.CGSize{Width: fieldWidth, Height: fieldHeight},
	})
	passwordField.SetStringValue(opts.ProvisionPassword)
	passwordField.SetPlaceholderString("optional")
	passwordField.SetEditable(true)
	passwordField.SetSelectable(true)
	passwordField.SetAccessibilityLabel("Password")
	passwordField.SetAccessibilityIdentifier("new-vm-password")
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), passwordField.ID)

	// Disclosure triangle + label.
	disclosureBtn := appkit.NewButtonWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 6},
		Size:   corefoundation.CGSize{Width: 13, Height: 13},
	})
	disclosureBtn.SetButtonType(appkit.NSButtonTypePushOnPushOff)
	disclosureBtn.SetBezelStyle(nsBezelStyleDisclosure)
	disclosureBtn.SetTitle("")
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), disclosureBtn.ID)

	disclosureLabel := appkit.NewTextFieldLabelWithString("Post-Install Scripts")
	disclosureLabel.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 18, Y: 6},
		Size:   corefoundation.CGSize{Width: 200, Height: 18},
	})
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), disclosureLabel.ID)

	sel.disclosureBtn = disclosureBtn
	sel.topSubviews = []appkit.NSView{
		lbl1.NSView, lbl2.NSView, lbl3.NSView,
		nameField.NSView, userField.NSView, passwordField.NSView,
		disclosureBtn.NSView, disclosureLabel.NSView,
	}

	// Recipe section (initially hidden, positioned at bottom of view).
	recipeSection := appkit.NewViewWithFrame(corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: viewWidth, Height: recipeSectionHeight},
	})
	recipeSection.SetHidden(true)
	sel.recipeSection = recipeSection

	// "Use defaults" checkbox.
	defaultsCheck := appkit.NewButtonCheckboxWithTitleTargetAction(
		"Use defaults (homebrew, golang, dev-tools)",
		nil, 0,
	)
	defaultsCheck.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 4, Y: recipeSectionHeight - 24},
		Size:   corefoundation.CGSize{Width: viewWidth - 8, Height: 20},
	})
	objc.Send[objc.ID](recipeSection.ID, objc.Sel("addSubview:"), defaultsCheck.ID)
	sel.defaultsCheck = defaultsCheck

	// Scroll view for recipe checkboxes.
	scrollFrame := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   corefoundation.CGSize{Width: viewWidth, Height: recipeSectionHeight - 30},
	}
	scrollView := appkit.NewScrollViewWithFrame(scrollFrame)
	scrollView.SetHasVerticalScroller(true)
	scrollView.SetAutohidesScrollers(true)

	// Document view containing all recipe rows.
	docHeight := float64(len(recipes)) * recipeRowH
	if docHeight < scrollFrame.Size.Height {
		docHeight = scrollFrame.Size.Height
	}
	docView := appkit.NewViewWithFrame(corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: viewWidth - 20, Height: docHeight},
	})

	for i, recipe := range recipes {
		y := docHeight - float64(i+1)*recipeRowH

		title := recipe.name
		if recipe.desc != "" {
			title = recipe.name + " — " + recipe.desc
		}
		cb := appkit.NewButtonCheckboxWithTitleTargetAction(title, nil, 0)
		cb.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 4, Y: y + depLabelH},
			Size:   corefoundation.CGSize{Width: viewWidth - 28, Height: checkboxRowH},
		})
		objc.Send[objc.ID](cb.ID, objc.Sel("setControlSize:"), appkit.NSControlSizeSmall)
		objc.Send[objc.ID](docView.ID, objc.Sel("addSubview:"), cb.ID)

		if len(recipe.requires) > 0 {
			depText := "requires: " + strings.Join(recipe.requires, ", ")
			depLabel := appkit.NewTextFieldLabelWithString(depText)
			depLabel.SetFrame(corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: 22, Y: y},
				Size:   corefoundation.CGSize{Width: viewWidth - 46, Height: depLabelH},
			})
			depLabel.SetFont(appkit.GetNSFontClass().SystemFontOfSize(10))
			depLabel.SetTextColor(appkit.GetNSColorClass().SecondaryLabelColor())
			objc.Send[objc.ID](docView.ID, objc.Sel("addSubview:"), depLabel.ID)
		}

		row := recipeCheckboxRow{info: recipe, checkbox: cb}
		sel.rows = append(sel.rows, row)
		sel.byName[recipe.name] = &sel.rows[len(sel.rows)-1]
	}

	scrollView.SetDocumentView(docView)
	objc.Send[objc.ID](recipeSection.ID, objc.Sel("addSubview:"), scrollView.ID)
	objc.Send[objc.ID](view.ID, objc.Sel("addSubview:"), recipeSection.ID)

	// Wire checkbox action handlers.
	for i := range sel.rows {
		row := &sel.rows[i]
		row.checkbox.SetActionHandler(func() {
			if row.checkbox.State() == appkit.NSControlStateValue(1) {
				sel.autoCheckDeps(row.info.name, map[string]bool{})
			}
			sel.updateDefaultsState()
		})
	}

	defaultsCheck.SetActionHandler(func() {
		on := defaultsCheck.State() == appkit.NSControlStateValue(1)
		for _, name := range defaultRecipePreset {
			if row, ok := sel.byName[name]; ok {
				if on {
					row.checkbox.SetState(appkit.NSControlStateValue(1))
				} else {
					row.checkbox.SetState(appkit.NSControlStateValue(0))
				}
			}
		}
	})

	// Pre-select from opts.
	for _, name := range splitRecipes(opts.PostInstallRecipes) {
		if row, ok := sel.byName[name]; ok {
			row.checkbox.SetState(appkit.NSControlStateValue(1))
		}
	}
	sel.updateDefaultsState()

	return view, nameField, userField, passwordField, sel
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
	recipes := loadRecipeInfos()
	for {
		alert := appkit.NewNSAlert()
		alert.SetMessageText("Create New VM")
		alert.SetInformativeText(fmt.Sprintf("Create in %s.\nOptional username/password creates an admin user.\nPost-install scripts run after first boot.", baseDir))
		alert.AddButtonWithTitle("Create")
		alert.AddButtonWithTitle("Cancel")
		accessoryView, nameField, userField, passwordField, recipeSel := newVMAccessoryView(opts, recipes)
		alert.SetAccessoryView(accessoryView)
		recipeSel.bindAlert(alert)

		if alert.RunModal() != appkit.AlertFirstButtonReturn {
			return newVMOptions{}, false
		}

		opts.Name = strings.TrimSpace(nameField.StringValue())
		opts.ProvisionUser = strings.TrimSpace(userField.StringValue())
		opts.ProvisionPassword = passwordField.StringValue()
		opts.ProvisionAdmin = true
		opts.PostInstallRecipes = recipeSel.selectedRecipes()

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
		vms:         vms,
		activeVM:    vmconfig.ActiveName(),
		selectedRow: -1,
		onSelect:    onSelect,
		onInstall:   onInstall,
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
			{Cmd: objc.RegisterName("tableView:viewForTableColumn:row:"), Fn: s.viewForTableColumn},
			{Cmd: objc.RegisterName("tableView:rowViewForRow:"), Fn: s.rowViewForRow},
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
	s.window.SetTitle("cove")
	s.window.SetBackgroundColor(appkit.GetNSColorClass().WindowBackgroundColor())
	s.window.SetMinSize(corefoundation.CGSize{Width: selectorMinWidth, Height: selectorMinHeight})
	s.window.Center()
	objc.Send[objc.ID](s.window.ID, objc.Sel("setReleasedWhenClosed:"), false)

	listWidth := float64(selectorWindowWidth - 2*selectorContentInset - selectorPanelGap - selectorDetailWidth)
	panelY := float64(selectorBarHeight + selectorContentInset)
	panelHeight := float64(selectorWindowHeight - selectorBarHeight - 2*selectorContentInset - selectorHeaderHeight - selectorPanelGap)
	headerY := panelY + panelHeight + selectorPanelGap
	detailX := float64(selectorContentInset) + listWidth + selectorPanelGap

	header := s.buildHeader(
		float64(selectorContentInset),
		headerY,
		float64(selectorWindowWidth-2*selectorContentInset),
		float64(selectorHeaderHeight),
	)
	listPanel := s.buildListPanel(
		float64(selectorContentInset),
		panelY,
		listWidth,
		panelHeight,
	)
	details := s.buildDetailsPanel(
		detailX,
		panelY,
		float64(selectorDetailWidth),
		panelHeight,
	)

	// Build button bar
	buttonBar := s.buildButtonBar()

	// Add subviews to the window's content view
	contentViewID := objc.Send[objc.ID](s.window.ID, objc.Sel("contentView"))
	objc.Send[objc.ID](contentViewID, objc.Sel("addSubview:"), header.ID)
	objc.Send[objc.ID](contentViewID, objc.Sel("addSubview:"), listPanel.ID)
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

	s.syncSelectedRow()
	s.updateListState()
	s.updateButtonStates()
}

func (s *VMSelector) buildHeader(x, y, width, height float64) appkit.NSView {
	header := appkit.NewViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: x, Y: y},
		Size:   corefoundation.CGSize{Width: width, Height: height},
	})
	objc.Send[objc.ID](header.ID, objc.Sel("retain"))
	objc.Send[objc.ID](header.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewMinY))

	title := selectorLabel(
		"cove",
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 0, Y: height - 28},
			Size:   corefoundation.CGSize{Width: width - 140, Height: 22},
		},
		appkit.GetNSFontClass().BoldSystemFontOfSize(22),
		appkit.GetNSColorClass().LabelColor(),
	)
	objc.Send[objc.ID](title.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth))
	objc.Send[objc.ID](header.ID, objc.Sel("addSubview:"), title.ID)

	subtitle := selectorLabel(
		"Select a VM to run, inspect, or manage.",
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 0, Y: 8},
			Size:   corefoundation.CGSize{Width: width - 180, Height: 16},
		},
		appkit.GetNSFontClass().SystemFontOfSize(13),
		appkit.GetNSColorClass().SecondaryLabelColor(),
	)
	objc.Send[objc.ID](subtitle.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth))
	objc.Send[objc.ID](header.ID, objc.Sel("addSubview:"), subtitle.ID)

	s.countLabel = selectorLabel(
		selectorVMCountText(len(s.vms)),
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: width - 120, Y: height - 22},
			Size:   corefoundation.CGSize{Width: 120, Height: 16},
		},
		appkit.GetNSFontClass().MonospacedDigitSystemFontOfSizeWeight(11, selectorMonospacedRegularWeight()),
		appkit.GetNSColorClass().SecondaryLabelColor(),
	)
	s.countLabel.SetAlignment(appkit.NSTextAlignmentRight)
	objc.Send[objc.ID](s.countLabel.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX))
	objc.Send[objc.ID](header.ID, objc.Sel("addSubview:"), s.countLabel.ID)

	return header
}

func (s *VMSelector) buildListPanel(x, y, width, height float64) appkit.NSView {
	panel := appkit.NewViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: x, Y: y},
		Size:   corefoundation.CGSize{Width: width, Height: height},
	})
	objc.Send[objc.ID](panel.ID, objc.Sel("retain"))
	objc.Send[objc.ID](panel.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewHeight))

	title := selectorLabel(
		"Virtual Machines",
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 0, Y: height - 18},
			Size:   corefoundation.CGSize{Width: width, Height: 18},
		},
		appkit.GetNSFontClass().BoldSystemFontOfSize(13),
		appkit.GetNSColorClass().LabelColor(),
	)
	objc.Send[objc.ID](title.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewMinY))
	objc.Send[objc.ID](panel.ID, objc.Sel("addSubview:"), title.ID)

	boxHeight := height - 18 - selectorSectionGap
	box := selectorPanel(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   corefoundation.CGSize{Width: width, Height: boxHeight},
	})
	box.SetBorderColor(appkit.GetNSColorClass().SeparatorColor().ColorWithAlphaComponent(0.06))
	box.SetFillColor(appkit.GetNSColorClass().ControlBackgroundColor().ColorWithAlphaComponent(0.28))
	objc.Send[objc.ID](box.ID, objc.Sel("retain"))
	objc.Send[objc.ID](box.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewHeight))
	objc.Send[objc.ID](panel.ID, objc.Sel("addSubview:"), box.ID)

	tableFrame := corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: width, Height: boxHeight},
	}
	s.tableView = appkit.NewTableViewWithFrame(tableFrame)
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("retain"))
	s.tableView.SetBackgroundColor(appkit.GetNSColorClass().WindowBackgroundColor().ColorWithAlphaComponent(0.01))
	s.tableView.SetUsesAlternatingRowBackgroundColors(false)
	s.tableView.SetAllowsEmptySelection(false)
	s.tableView.SetIntercellSpacing(corefoundation.CGSize{Width: 0, Height: 8})
	s.tableView.SetRowHeight(selectorRowHeight)
	s.tableView.SetSelectionHighlightStyle(appkit.NSTableViewSelectionHighlightStyleRegular)
	s.tableView.SetColumnAutoresizingStyle(appkit.NSTableViewFirstColumnOnlyAutoresizingStyle)

	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setHeaderView:"), objc.ID(0))
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setDataSource:"), s.delegateID)
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setDelegate:"), s.delegateID)
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setTarget:"), s.delegateID)
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("setDoubleAction:"), objc.Sel("runVM:"))
	s.addColumn("vm", "", width-20, true)

	s.listScroll = appkit.NewScrollViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   corefoundation.CGSize{Width: width, Height: boxHeight},
	})
	objc.Send[objc.ID](s.listScroll.ID, objc.Sel("retain"))
	s.listScroll.SetDrawsBackground(false)
	s.listScroll.SetBorderType(appkit.NSNoBorder)
	objc.Send[objc.ID](s.listScroll.ID, objc.Sel("setHasVerticalScroller:"), true)
	objc.Send[objc.ID](s.listScroll.ID, objc.Sel("setDocumentView:"), s.tableView.ID)
	objc.Send[objc.ID](s.listScroll.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewHeight))
	objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), s.listScroll.ID)

	s.emptyState = appkit.NewViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 18, Y: 18},
		Size:   corefoundation.CGSize{Width: width - 36, Height: boxHeight - 36},
	})
	objc.Send[objc.ID](s.emptyState.ID, objc.Sel("retain"))
	objc.Send[objc.ID](s.emptyState.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewHeight))
	objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), s.emptyState.ID)

	emptyTitle := selectorLabel(
		"No virtual machines found.",
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 0, Y: (boxHeight - 36) / 2},
			Size:   corefoundation.CGSize{Width: width - 36, Height: 20},
		},
		appkit.GetNSFontClass().BoldSystemFontOfSize(16),
		appkit.GetNSColorClass().LabelColor(),
	)
	emptyTitle.SetAlignment(appkit.NSTextAlignmentCenter)
	objc.Send[objc.ID](s.emptyState.ID, objc.Sel("addSubview:"), emptyTitle.ID)

	emptySubtitle := selectorLabel(
		"Create one with New VM to get started.",
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 0, Y: (boxHeight-36)/2 - 24},
			Size:   corefoundation.CGSize{Width: width - 36, Height: 18},
		},
		appkit.GetNSFontClass().SystemFontOfSize(13),
		appkit.GetNSColorClass().SecondaryLabelColor(),
	)
	emptySubtitle.SetAlignment(appkit.NSTextAlignmentCenter)
	objc.Send[objc.ID](s.emptyState.ID, objc.Sel("addSubview:"), emptySubtitle.ID)

	return panel
}

func (s *VMSelector) buildDetailsPanel(x, y, width, height float64) appkit.NSView {
	panel := appkit.NewViewWithFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: x, Y: y},
		Size:   corefoundation.CGSize{Width: width, Height: height},
	})
	objc.Send[objc.ID](panel.ID, objc.Sel("retain"))
	objc.Send[objc.ID](panel.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX|selectorViewHeight))

	title := selectorLabel(
		"Details",
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 0, Y: height - 18},
			Size:   corefoundation.CGSize{Width: width, Height: 18},
		},
		appkit.GetNSFontClass().BoldSystemFontOfSize(13),
		appkit.GetNSColorClass().LabelColor(),
	)
	objc.Send[objc.ID](title.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewMinY))
	objc.Send[objc.ID](panel.ID, objc.Sel("addSubview:"), title.ID)

	boxHeight := height - 18 - selectorSectionGap
	box := selectorPanel(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   corefoundation.CGSize{Width: width, Height: boxHeight},
	})
	box.SetBorderColor(appkit.GetNSColorClass().SeparatorColor().ColorWithAlphaComponent(0.08))
	box.SetFillColor(appkit.GetNSColorClass().ControlBackgroundColor().ColorWithAlphaComponent(0.42))
	objc.Send[objc.ID](box.ID, objc.Sel("retain"))
	objc.Send[objc.ID](box.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewHeight))
	objc.Send[objc.ID](panel.ID, objc.Sel("addSubview:"), box.ID)

	labelColor := appkit.GetNSColorClass().LabelColor()
	secondaryColor := appkit.GetNSColorClass().SecondaryLabelColor()
	valueFont := appkit.GetNSFontClass().BoldSystemFontOfSize(13)

	s.detailTitle = selectorLabel(
		"No VM Selected",
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 18, Y: boxHeight - 40},
			Size:   corefoundation.CGSize{Width: width - 36, Height: 24},
		},
		appkit.GetNSFontClass().BoldSystemFontOfSize(20),
		labelColor,
	)
	s.detailTitle.SetAlignment(appkit.NSTextAlignmentLeft)
	objc.Send[objc.ID](s.detailTitle.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewMinY))
	objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), s.detailTitle.ID)

	addDetailRow := func(label string, y float64) appkit.NSTextField {
		left := selectorLabel(
			label,
			corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: 18, Y: y},
				Size:   corefoundation.CGSize{Width: 62, Height: 18},
			},
			appkit.GetNSFontClass().SystemFontOfSize(13),
			secondaryColor,
		)
		objc.Send[objc.ID](left.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinY))
		objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), left.ID)

		value := selectorLabel(
			"",
			corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: 90, Y: y},
				Size:   corefoundation.CGSize{Width: width - 108, Height: 18},
			},
			valueFont,
			labelColor,
		)
		value.SetAlignment(appkit.NSTextAlignmentRight)
		objc.Send[objc.ID](value.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX|selectorViewMinY))
		objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), value.ID)
		return value
	}

	s.detailState = addDetailRow("Status", boxHeight-78)
	s.detailOS = addDetailRow("OS", boxHeight-104)
	s.detailSize = addDetailRow("Disk", boxHeight-130)
	s.detailDate = addDetailRow("Created", boxHeight-156)

	pathLabel := selectorLabel(
		"Path",
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 18, Y: boxHeight - 194},
			Size:   corefoundation.CGSize{Width: width - 36, Height: 18},
		},
		appkit.GetNSFontClass().SystemFontOfSize(13),
		secondaryColor,
	)
	objc.Send[objc.ID](pathLabel.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewMinY))
	objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), pathLabel.ID)

	s.detailPath = selectorLabel(
		"",
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 18, Y: boxHeight - 226},
			Size:   corefoundation.CGSize{Width: width - 36, Height: 34},
		},
		appkit.GetNSFontClass().SystemFontOfSize(11),
		secondaryColor,
	)
	s.detailPath.SetAlignment(appkit.NSTextAlignmentLeft)
	s.detailPath.SetUsesSingleLineMode(false)
	s.detailPath.SetLineBreakMode(appkit.NSLineBreakByTruncatingMiddle)
	objc.Send[objc.ID](s.detailPath.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewMinY))
	objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), s.detailPath.ID)

	target := objectivec.ObjectFromID(s.delegateID)
	s.revealButton = appkit.NewButtonWithTitleTargetAction("Reveal in Finder", target, objc.Sel("revealVMInFinder:"))
	s.revealButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 18, Y: 52},
		Size:   corefoundation.CGSize{Width: width - 36, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.revealButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](s.revealButton.ID, objc.Sel("setEnabled:"), false)
	objc.Send[objc.ID](s.revealButton.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX))
	objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), s.revealButton.ID)

	s.scriptButton = appkit.NewButtonWithTitleTargetAction("Run VZScript...", target, objc.Sel("openVZScriptRunner:"))
	s.scriptButton.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 18, Y: 16},
		Size:   corefoundation.CGSize{Width: width - 36, Height: selectorButtonHeight},
	})
	objc.Send[objc.ID](s.scriptButton.ID, objc.Sel("setBezelStyle:"), int(1))
	objc.Send[objc.ID](s.scriptButton.ID, objc.Sel("setEnabled:"), false)
	objc.Send[objc.ID](s.scriptButton.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX))
	objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), s.scriptButton.ID)

	return panel
}

func (s *VMSelector) updateListState() {
	if s.countLabel.ID != 0 {
		s.countLabel.SetStringValue(selectorVMCountText(len(s.vms)))
	}
	if s.listScroll.ID == 0 || s.emptyState.ID == 0 {
		return
	}
	hasVMs := len(s.vms) > 0
	s.listScroll.SetHidden(!hasVMs)
	s.emptyState.SetHidden(hasVMs)
}

func (s *VMSelector) reloadRowCards(rows ...int) {
	if s.tableView.ID == 0 {
		return
	}

	rowIndexes := foundation.NewNSMutableIndexSet()
	added := false
	for _, row := range rows {
		if row < 0 || row >= len(s.vms) {
			continue
		}
		rowIndexes.AddIndex(uint(row))
		added = true
	}
	if !added {
		return
	}

	columnIndexes := foundation.NewMutableIndexSetWithIndex(0)
	s.tableView.ReloadDataForRowIndexesColumnIndexes(rowIndexes.NSIndexSet, columnIndexes.NSIndexSet)
	s.updateRowViews(rows...)
}

func (s *VMSelector) syncSelectedRow() {
	row := int(objc.Send[int64](s.tableView.ID, objc.Sel("selectedRow")))
	s.reloadRowCards(s.selectedRow, row)
	s.selectedRow = row
}

func (s *VMSelector) updateRowViews(rows ...int) {}

func (s *VMSelector) rowViewForRow(_ objc.ID, _ objc.SEL, _ objc.ID, row int) objc.ID {
	rowView := appkit.NewNSTableRowView()
	if row >= 0 && row < len(s.vms) {
		rowView.SetAccessibilityLabel(s.vms[row].Name)
		rowView.SetAccessibilityIdentifier(s.vms[row].Name)
	}
	return rowView.ID
}

func (s *VMSelector) viewForTableColumn(_ objc.ID, _ objc.SEL, _ objc.ID, colID objc.ID, row int) objc.ID {
	if row < 0 || row >= len(s.vms) {
		return 0
	}
	vm := s.vms[row]
	column := appkit.NSTableColumnFromID(colID)
	columnWidth := column.Width()
	if columnWidth < 280 {
		columnWidth = 280
	}

	cell := appkit.NewTableCellViewWithFrame(corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: columnWidth, Height: selectorRowHeight},
	})

	contentX := 22.0
	contentRightInset := 18.0

	title := selectorLabel(
		selectorRowTitle(vm, s.activeVM),
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: contentX, Y: 30},
			Size:   corefoundation.CGSize{Width: columnWidth - contentX - 110, Height: 18},
		},
		appkit.GetNSFontClass().BoldSystemFontOfSize(14),
		appkit.GetNSColorClass().LabelColor(),
	)
	title.SetUsesSingleLineMode(true)
	title.SetLineBreakMode(appkit.NSLineBreakByTruncatingTail)
	objc.Send[objc.ID](title.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth))
	objc.Send[objc.ID](cell.ID, objc.Sel("addSubview:"), title.ID)
	cell.SetTextField(title)

	status := selectorLabel(
		selectorStateText(vm),
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: columnWidth - 82 - contentRightInset, Y: 31},
			Size:   corefoundation.CGSize{Width: 82, Height: 14},
		},
		appkit.GetNSFontClass().SystemFontOfSize(11),
		appkit.GetNSColorClass().TertiaryLabelColor(),
	)
	status.SetAlignment(appkit.NSTextAlignmentRight)
	objc.Send[objc.ID](status.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX))
	objc.Send[objc.ID](cell.ID, objc.Sel("addSubview:"), status.ID)

	subtitle := selectorLabel(
		selectorRowSubtitle(vm),
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: contentX, Y: 12},
			Size:   corefoundation.CGSize{Width: columnWidth - contentX - contentRightInset, Height: 15},
		},
		appkit.GetNSFontClass().SystemFontOfSize(12),
		appkit.GetNSColorClass().SecondaryLabelColor(),
	)
	subtitle.SetUsesSingleLineMode(true)
	subtitle.SetLineBreakMode(appkit.NSLineBreakByTruncatingTail)
	objc.Send[objc.ID](subtitle.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth))
	objc.Send[objc.ID](cell.ID, objc.Sel("addSubview:"), subtitle.ID)

	return cell.ID
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
	objc.Send[objc.ID](s.scriptButton.ID, objc.Sel("setEnabled:"), canOpenVZScriptRunner(vm))
	s.updateDetailsPanel(vm)
}

func canOpenVZScriptRunner(vm *VMInfo) bool {
	return vm != nil && vm.State == "running"
}

func (s *VMSelector) updateDetailsPanel(vm *VMInfo) {
	if vm == nil {
		s.detailTitle.SetStringValue("No VM Selected")
		s.detailState.SetStringValue("-")
		s.detailOS.SetStringValue("-")
		s.detailSize.SetStringValue("-")
		s.detailDate.SetStringValue("-")
		s.detailPath.SetStringValue("")
		return
	}

	title := vm.Name
	if vm.Name == s.activeVM {
		title += " (active)"
	}
	s.detailTitle.SetStringValue(title)
	s.detailState.SetStringValue(selectorStateText(*vm))
	s.detailOS.SetStringValue(vm.OSType)
	s.detailSize.SetStringValue(bytefmt.Size(vm.DiskSize))
	s.detailDate.SetStringValue(vm.Created.Format("2006-01-02 15:04"))
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
	s.activeVM = vmconfig.ActiveName()
	s.updateListState()
	objc.Send[objc.ID](s.tableView.ID, objc.Sel("reloadData"))

	if !s.selectRowByName(selectedName) && !s.selectRowByName(s.activeVM) {
		if row := s.initialSelectionRow(); row >= 0 {
			indexSet := objc.Send[objc.ID](
				objc.Send[objc.ID](objc.ID(objc.GetClass("NSIndexSet")), objc.Sel("alloc")),
				objc.Sel("initWithIndex:"), uint(row),
			)
			objc.Send[objc.ID](s.tableView.ID, objc.Sel("selectRowIndexes:byExtendingSelection:"), indexSet, false)
		} else {
			s.selectedRow = -1
		}
	}
	s.syncSelectedRow()
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
	case "vm":
		value = selectorRowTitle(vm, s.activeVM)
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
		value = bytefmt.Size(vm.DiskSize)
	case "created":
		value = vm.Created.Format("2006-01-02")
	}
	return objc.String(value)
}

// NSTableViewDelegate: tableViewSelectionDidChange:
func (s *VMSelector) selectionDidChange(_ objc.ID, _ objc.SEL, _ objc.ID) {
	s.syncSelectedRow()
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

		// Save selected recipes to config.json so they survive failures.
		if postInstallRecipes != "" {
			savePostInstallRecipes(vmDir, postInstallRecipes)
		}

		if errors.Is(err, errRestartVM) {
			if setErr := vmconfig.SetActive(vmName); setErr != nil {
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
		if setErr := vmconfig.SetActive(vmName); setErr != nil {
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
	ensureAppLaunched(app)

	for {
		var action selectorAction
		var appLoopStop atomic.Bool

		// Declare selector before the closures that reference it.
		var selector *VMSelector
		selector = NewVMSelector(vms, func(vm VMInfo, coldBoot bool) {
			action = selectorAction{
				kind:     selectorActionRun,
				vm:       vm,
				coldBoot: coldBoot,
			}
			objc.Send[objc.ID](selector.window.ID, objc.Sel("close"))
			appLoopStop.Store(true)
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
			appLoopStop.Store(true)
			postDummyEvent(app)
		})
		setupSelectorMenu(selector.delegateID)

		// Quit when the selector window closes.
		nc := foundation.GetNotificationCenterClass().DefaultCenter()
		nsName := objc.String("NSWindowWillCloseNotification")
		objc.Send[objc.ID](nc.ID, objc.Sel("addObserverForName:object:queue:usingBlock:"),
			nsName, selector.window.ID, objc.ID(0),
			objc.NewBlock(func(_ objc.Block, _ objc.ID) {
				appLoopStop.Store(true)
				postDummyEvent(app)
			}),
		)

		selector.Show()
		runAppEventLoopUntil(app, appLoopStop.Load)

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
