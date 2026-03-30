// installer.go - macOS installation following Code-Hex/vz patterns
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/symbols"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"
)

// guiUpdate holds pending UI changes that background goroutines request.
// The main thread polls this and applies updates, avoiding purego callbacks
// on the main queue (which cause GC stack corruption with app.Run()).
type guiUpdate struct {
	mu    sync.Mutex
	dirty bool // true if any field changed since last apply

	// Progress window state.
	statusText  string
	statusPct   float64 // <0 means indeterminate
	windowTitle string

	// One-shot actions (cleared after apply).
	closeProgressWindow bool
	createVMWindow      bool
	setVMWindowTitle    string
	fadeOutOverlay      bool
	stopApp             bool
}

// setProgress updates the progress status from a background goroutine.
func (g *guiUpdate) setProgress(text string, pct float64) {
	g.mu.Lock()
	g.statusText = text
	g.statusPct = pct
	g.dirty = true
	g.mu.Unlock()
}

// setWindowTitle requests a window title change.
func (g *guiUpdate) setWindowTitle(title string) {
	g.mu.Lock()
	g.windowTitle = title
	g.dirty = true
	g.mu.Unlock()
}

// requestCloseProgressWindow signals the main thread to close the progress window.
func (g *guiUpdate) requestCloseProgressWindow() {
	g.mu.Lock()
	g.closeProgressWindow = true
	g.dirty = true
	g.mu.Unlock()
}

// requestCreateVMWindow signals the main thread to create the VM window.
func (g *guiUpdate) requestCreateVMWindow() {
	g.mu.Lock()
	g.createVMWindow = true
	g.dirty = true
	g.mu.Unlock()
}

// requestSetVMWindowTitle sets the VM window title.
func (g *guiUpdate) requestSetVMWindowTitle(title string) {
	g.mu.Lock()
	g.setVMWindowTitle = title
	g.dirty = true
	g.mu.Unlock()
}

// requestFadeOutOverlay signals the main thread to fade out the install overlay.
func (g *guiUpdate) requestFadeOutOverlay() {
	g.mu.Lock()
	g.fadeOutOverlay = true
	g.dirty = true
	g.mu.Unlock()
}

// requestStopApp signals the main thread to exit the event loop.
func (g *guiUpdate) requestStopApp() {
	g.mu.Lock()
	g.stopApp = true
	g.dirty = true
	g.mu.Unlock()
}

// errRestartVM is a sentinel error returned by runInstallationWithGUI to signal
// that installation succeeded and the caller should start the VM with GUI.
var errRestartVM = errors.New("restart VM")

// injectSucceededMarker returns the path to the marker file that records
// whether disk injection completed successfully.
func injectSucceededMarker() string { return filepath.Join(vmDir, ".inject-succeeded") }

// markInjectSucceeded creates the inject success marker file.
func markInjectSucceeded() {
	if err := os.WriteFile(injectSucceededMarker(), nil, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: mark inject succeeded: %v\n", err)
	}
}

// clearInjectSucceeded removes the inject success marker file.
func clearInjectSucceeded() { os.Remove(injectSucceededMarker()) }

// didInjectSucceed reports whether disk injection succeeded for this VM.
func didInjectSucceed() bool { _, err := os.Stat(injectSucceededMarker()); return err == nil }

// stopVMAndInject stops a running VM, waits for the disk to be released, and
// optionally injects provisioning files if provisionUser/provisionPassword are set.
func stopVMAndInject(vm *virtualMachine) {
	fmt.Println("Stopping VM...")
	stopDone := make(chan struct{})
	DispatchAsyncQueue(vm.queue, func() {
		vm.vm.StopWithCompletionHandler(func(err error) {
			if err != nil {
				vzlog("VM stop error: %v", err)
			}
			close(stopDone)
		})
	})
	select {
	case <-stopDone:
		fmt.Println("VM stopped.")
	case <-time.After(30 * time.Second):
		fmt.Println("VM stop timed out, continuing...")
	}

	// Wait for the disk to be released instead of a fixed sleep. The VZ
	// framework may hold the file handle briefly after stop returns.
	diskFile := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskFile); err != nil {
		fmt.Printf("warning: disk not found after VM stop: %s (%v)\n", diskFile, err)
		fmt.Printf("  vmDir=%s\n", vmDir)
		// List what's actually in the directory.
		if entries, derr := os.ReadDir(vmDir); derr == nil {
			for _, e := range entries {
				fmt.Printf("  - %s\n", e.Name())
			}
		} else {
			fmt.Printf("  cannot list vmDir: %v\n", derr)
		}
	}
	if err := waitForDiskAvailable(diskFile, 15*time.Second); err != nil {
		fmt.Printf("warning: %v\n", err)
	}

	if provisionUser == "" || provisionPassword == "" {
		return
	}

	if provisionStrategy == "gui" {
		fmt.Println("Skipping disk provisioning (strategy=gui).")
		return
	}

	fmt.Println()
	fmt.Println("Provisioning VM disk...")
	injectOpts := InjectOptions{
		Config: ProvisionConfig{
			Username: provisionUser,
			Password: provisionPassword,
			Admin:    provisionAdmin,
		},
		SkipSetupAssistant: true,
		AutoLogin:          true,
		InjectAgent:        true,
		InjectGuestTools:   true,
	}
	if err := injectProvisioningFilesWithOptions(injectOpts); err != nil {
		fmt.Printf("warning: disk provisioning failed: %v\n", err)
		if provisionStrategy == "auto" {
			fmt.Println("Will fall back to GUI automation on first boot.")
		} else {
			exe, _ := os.Executable()
			fmt.Println("You can provision later with:")
			fmt.Printf("  %s", exe)
			if vmName != "" {
				fmt.Printf(" -vm %s", vmName)
			}
			fmt.Printf(" provision -user %s -password <password> -skip-setup-assistant\n", provisionUser)
		}
	} else {
		markInjectSucceeded()
	}
	fmt.Println()
}

// installMacOSLikeVZ installs macOS using the flow originally modeled on
// Code-Hex/vz, the CGO reference implementation for Virtualization.framework.
// The name is historical. The flow keeps the same core setup:
// 1. Uses a local restore image path instead of a restore image URL.
// 2. Creates platform config from the IPSW's hardware requirements.
// 3. Uses the simpler VM creation and installer flow.

// checkExistingVM checks if a VM disk already exists in the VM directory.
// It returns an error if disk.img exists and -force was not specified,
// preventing accidental data loss from re-installing over an existing VM.
func checkExistingVM(dir string, diskName string) error {
	diskFile := filepath.Join(dir, diskName)
	info, err := os.Stat(diskFile)
	if err != nil {
		return nil // doesn't exist, safe to proceed
	}
	if forceInstall {
		fmt.Printf("warning: overwriting existing disk %s (%d MB)\n", diskFile, info.Size()/(1024*1024))
		if err := os.Remove(diskFile); err != nil {
			return fmt.Errorf("remove existing disk: %w", err)
		}
		// Clear stale provisioning marker from previous install.
		clearInjectSucceeded()
		return nil
	}
	return fmt.Errorf("vm disk already exists: %s (%d MB)\n\nTo install over this disk, use -force (THIS WILL DESTROY ALL DATA IN THE VM).\nTo use a different VM, use -vm <name>", diskFile, info.Size()/(1024*1024))
}

func installMacOSLikeVZ(ctx context.Context) error {
	fmt.Println("=== macOS Installation ===")

	// Safety check: refuse to overwrite existing VM disk unless -force is specified.
	if err := checkExistingVM(vmDir, "disk.img"); err != nil {
		return err
	}

	// Step 1: Create VM bundle directory
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}

	// In GUI mode, the entire lifecycle (download → install → first boot)
	// happens within a single window. Hand off to the GUI installer early.
	if guiMode {
		return runFullInstallWithGUI(ctx)
	}

	// Headless path: download → install sequentially with stdout progress.
	restoreImagePath, err := resolveOrDownloadIPSW(ctx)
	if err != nil {
		return err
	}

	installer, err := prepareInstaller(ctx, restoreImagePath)
	if err != nil {
		return err
	}

	fmt.Println("Starting installation...")
	if err := runInstallation(ctx, installer); err != nil {
		return err
	}

	// If --unattended, automatically boot the VM for first-boot setup.
	if unattended {
		fmt.Println("Unattended mode: booting VM for first-boot setup...")
		guiMode = true // need GUI for screenshots + keyboard injection
		return runMacOSVM()
	}

	// If GUI provisioning is needed, auto-boot with GUI.
	if provisionUser != "" && (provisionStrategy == "gui" || (provisionStrategy == "auto" && !didInjectSucceed())) {
		fmt.Println("Booting VM for GUI provisioning...")
		guiMode = true
		return runMacOSVM()
	}
	return nil
}

// runFullInstallWithGUI performs the complete install lifecycle with GUI:
// 1. A compact progress window during download/preparation
// 2. The full VM window during installation and first boot
//
// UI updates use a polling pattern instead of DispatchAsync(GetMainDispatchQueue())
// to avoid purego callback GC stack corruption ("bad pointer in reflect.Value.call").
// Background goroutines write to a shared guiUpdate struct; the main thread reads
// it each iteration of the event loop and applies changes directly.
func runFullInstallWithGUI(ctx context.Context) error {
	// Set up NSApplication early.
	app := getSharedApp()
	if app.ID == 0 {
		return fmt.Errorf("failed to create NSApplication")
	}
	app.SetActivationPolicy(appkit.NSApplicationActivationPolicyRegular)
	if !appFinishedLaunching {
		// Use "run-then-stop" to fully initialize the NSApplication event
		// machinery (see runVMWithGUI for full explanation).
		foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(0, false, func(_ *foundation.NSTimer) {
			app.Stop(nil)
			postDummyEvent(app)
		})
		app.Run()
		appFinishedLaunching = true
	}

	// Create a compact progress window for the download/prep phase.
	progressWindow, statusLabel, progressBarID := createProgressWindow()
	progressWindow.MakeKeyAndOrderFront(nil)
	app.Activate()

	// Shared state for background → main thread communication.
	var ui guiUpdate
	ui.statusPct = -1

	// Lifecycle result communicated via mutex-protected error.
	var lifecycleErr error
	var lifecycleErrMu sync.Mutex

	// Shared state for the VM window (created on main thread, referenced by both).
	var vmWindow appkit.NSWindow
	var vmOverlay appkit.NSView
	vmWindowCreated := make(chan struct{})
	var installerRef *macOSInstaller

	// Helper for the background goroutine to set progress.
	setStatus := func(text string, pct float64) {
		ui.setProgress(text, pct)
	}

	// Run the full lifecycle in a background goroutine.
	go func() {
		defer ui.requestStopApp()

		// Phase 1: Resolve or download IPSW.
		setStatus("Checking for cached restore image...", -1)
		restoreImagePath, err := resolveOrDownloadIPSWWithProgress(ctx, setStatus)
		if err != nil {
			lifecycleErrMu.Lock()
			lifecycleErr = err
			lifecycleErrMu.Unlock()
			return
		}

		// Phase 2: Load and configure VM.
		setStatus("Loading restore image...", -1)
		ui.setWindowTitle("vz-macos - Loading Restore Image")
		fmt.Println("Loading restore image...")
		installer, err := prepareInstaller(ctx, restoreImagePath)
		if err != nil {
			lifecycleErrMu.Lock()
			lifecycleErr = err
			lifecycleErrMu.Unlock()
			return
		}
		installerRef = installer

		// Phase 3: Signal main thread to close progress window and create VM window.
		setStatus("Starting installation...", 100)
		fmt.Println("Starting installation...")
		ui.requestCloseProgressWindow()
		ui.requestCreateVMWindow()

		// Wait for the main thread to create the VM window before proceeding.
		<-vmWindowCreated

		// Phase 4: Run installation with progress monitoring.
		doneCh := make(chan error, 1)
		DispatchSync(uintptr(installer.vm.queue.Handle()), func() {
			installer.installer.InstallWithCompletionHandler(func(err error) {
				doneCh <- err
			})
		})

		installProgress := installer.installer.Progress()
		if installProgress.GetID() == 0 {
			lifecycleErrMu.Lock()
			lifecycleErr = fmt.Errorf("installer has no progress object")
			lifecycleErrMu.Unlock()
			return
		}

		overlayVisible := true
		lastPercent := -1.0

		for {
			select {
			case err := <-doneCh:
				fmt.Println() // newline after progress bar
				if err != nil {
					printDetailedInstallError(err)
					ui.requestSetVMWindowTitle("macOS VM Installation - FAILED")
					lifecycleErrMu.Lock()
					lifecycleErr = err
					lifecycleErrMu.Unlock()
					return
				}
				fmt.Println("=== Installation Complete ===")
				ui.requestSetVMWindowTitle("macOS VM Installation - Stopping VM...")

				stopVMAndInject(installer.vm)

				ui.requestSetVMWindowTitle("macOS VM Installation - Restarting...")
				fmt.Println("Restarting VM for first boot...")
				lifecycleErrMu.Lock()
				lifecycleErr = errRestartVM
				lifecycleErrMu.Unlock()
				return

			case <-ctx.Done():
				lifecycleErrMu.Lock()
				lifecycleErr = ctx.Err()
				lifecycleErrMu.Unlock()
				return

			default:
				currentPercent := installProgress.FractionCompleted() * 100
				if currentPercent-lastPercent >= 1.0 || lastPercent < 0 {
					printProgress(currentPercent)
					ui.requestSetVMWindowTitle(fmt.Sprintf("macOS VM Installation - %.1f%%", currentPercent))
					if overlayVisible && currentPercent > 0 {
						overlayVisible = false
						ui.requestFadeOutOverlay()
					}
					lastPercent = currentPercent
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
	}()

	// Repeating NSTimer handles UI updates on the main thread at ~30 Hz.
	// This runs inside app.Run()'s event loop, which properly routes
	// window server events (mouse, keyboard, Cmd+Tab) — unlike a manual
	// NextEventMatchingMask/SendEvent loop which never receives events.
	var overlayFadeStep int = -1 // -1 means not fading
	foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(
		0.033, // ~30 Hz
		true,
		func(_ *foundation.NSTimer) {
			// Apply pending UI updates from background goroutines.
			ui.mu.Lock()
			if ui.dirty || overlayFadeStep >= 0 {
				ui.dirty = false

				// Progress window updates.
				if statusLabel != 0 && ui.statusText != "" {
					objc.Send[objc.ID](statusLabel, objc.Sel("setStringValue:"), objc.String(ui.statusText))
				}
				if progressBarID != 0 {
					if ui.statusPct < 0 {
						objc.Send[objc.ID](progressBarID, objc.Sel("setIndeterminate:"), true)
						objc.Send[objc.ID](progressBarID, objc.Sel("startAnimation:"), objc.ID(0))
					} else {
						objc.Send[objc.ID](progressBarID, objc.Sel("setIndeterminate:"), false)
						objc.Send[objc.ID](progressBarID, objc.Sel("setDoubleValue:"), ui.statusPct)
					}
				}

				// Window title change.
				if ui.windowTitle != "" {
					progressWindow.SetTitle(ui.windowTitle)
					ui.windowTitle = ""
				}

				// Close progress window.
				if ui.closeProgressWindow {
					progressWindow.Close()
					ui.closeProgressWindow = false
				}

				// Create VM window.
				if ui.createVMWindow && installerRef != nil {
					contentRect := corefoundation.CGRect{
						Origin: corefoundation.CGPoint{X: 100, Y: 100},
						Size:   corefoundation.CGSize{Width: 1024, Height: 768},
					}
					vmView := vz.NewVZVirtualMachineView()
					vmView.SetVirtualMachine(&installerRef.vm.vm)
					vmView.SetCapturesSystemKeys(true)
					vmView.SetAutomaticallyReconfiguresDisplay(true)
					vmViewAsNSView(vmView).SetFrame(corefoundation.CGRect{
						Origin: corefoundation.CGPoint{X: 0, Y: 0},
						Size:   contentRect.Size,
					})

					vmWindow = appkit.NewWindowWithContentRectStyleMaskBackingDefer(
						contentRect,
						appkit.NSWindowStyleMaskTitled|appkit.NSWindowStyleMaskClosable|appkit.NSWindowStyleMaskMiniaturizable|appkit.NSWindowStyleMaskResizable,
						appkit.NSBackingStoreBuffered,
						false,
					)
					vmWindow.SetTitleVisibility(appkit.NSWindowTitleVisible)
					vmWindow.SetTitlebarAppearsTransparent(false)
					vmWindow.SetTitle("macOS VM Installation")
					vmWindow.SetContentView(vmViewAsNSView(vmView))
					vmWindow.Center()

					func() {
						defer func() {
							if r := recover(); r != nil {
								fmt.Fprintf(os.Stderr, "warning: install overlay skipped: %v\n", r)
							}
						}()
						vmOverlay = createInstallOverlay(contentRect.Size)
						addSubview(vmViewAsNSView(vmView), vmOverlay)
					}()

					vmWindow.MakeKeyAndOrderFront(nil)
					vmWindow.MakeFirstResponder(vmViewAsNSView(vmView).NSResponder)
					close(vmWindowCreated)
					ui.createVMWindow = false
				}

				// VM window title.
				if ui.setVMWindowTitle != "" && vmWindow.ID != 0 {
					vmWindow.SetTitle(ui.setVMWindowTitle)
					ui.setVMWindowTitle = ""
				}

				// Fade out overlay — animate over ~10 iterations (~0.33s at 30 Hz).
				if ui.fadeOutOverlay && vmOverlay.ID != 0 {
					overlayFadeStep = 10
					ui.fadeOutOverlay = false
				}
				if overlayFadeStep >= 0 && vmOverlay.ID != 0 {
					alpha := float64(overlayFadeStep) / 10.0
					objc.Send[objc.ID](vmOverlay.ID, objc.Sel("setAlphaValue:"), alpha)
					overlayFadeStep--
					if overlayFadeStep < 0 {
						objc.Send[objc.ID](vmOverlay.ID, objc.Sel("removeFromSuperview"))
					}
				}

				// Stop the app when lifecycle is done.
				if ui.stopApp {
					ui.stopApp = false
					app.Stop(nil)
					postDummyEvent(app)
				}
			}
			ui.mu.Unlock()
		},
	)

	// app.Run() blocks until app.Stop() is called (when lifecycle completes).
	// This properly routes all window server events (mouse, keyboard, Cmd+Tab).
	app.Run()

	// Close any windows left over from the install phase so the run phase
	// starts with a clean slate (avoids stale black windows).
	if vmWindow.ID != 0 {
		vmWindow.Close()
	}
	if progressWindow.ID != 0 {
		progressWindow.Close()
	}

	lifecycleErrMu.Lock()
	defer lifecycleErrMu.Unlock()
	return lifecycleErr
}

// createProgressWindow creates a compact macOS-style progress window for
// download and preparation phases. Includes an SF Symbol icon with pulse effect.
func createProgressWindow() (appkit.NSWindow, objc.ID, objc.ID) {
	const (
		winW, winH = 420, 110
		iconSize   = 48.0
		iconLeft   = 20.0
		textLeft   = iconLeft + iconSize + 12 // right of icon + gap
		textWidth  = winW - textLeft - 20
	)

	contentRect := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 200, Y: 400},
		Size:   corefoundation.CGSize{Width: winW, Height: winH},
	}
	styleMask := appkit.NSWindowStyleMaskTitled | appkit.NSWindowStyleMaskClosable | appkit.NSWindowStyleMaskMiniaturizable
	window := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
		contentRect,
		styleMask,
		appkit.NSBackingStoreBuffered,
		false,
	)
	window.SetStyleMask(styleMask)
	window.SetTitle("vz-macos - Downloading macOS Installer")
	window.SetTitleVisibility(appkit.NSWindowTitleVisible)
	window.SetTitlebarAppearsTransparent(false)
	window.Center()

	// Content view with padding. The content rect doesn't include the title
	// bar, so all coordinates are relative to the content area bottom-left.
	contentView := appkit.NewViewWithFrame(corefoundation.CGRect{
		Size: corefoundation.CGSize{Width: winW, Height: winH},
	})

	// SF Symbol icon (laptopcomputer.and.arrow.down / 􀶿) on the left.
	symbolImg := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(
		"laptopcomputer.and.arrow.down", "Download to Mac",
	)
	if symbolImg.ID != 0 {
		// Configure the symbol to render at a large point size.
		symbolConfig := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSImageSymbolConfiguration")),
			objc.Sel("configurationWithPointSize:weight:"),
			32.0, int64(0), // 32pt, regular weight
		)
		largeSymbol := objc.Send[objc.ID](symbolImg.ID,
			objc.Sel("imageWithSymbolConfiguration:"), symbolConfig,
		)
		if largeSymbol != 0 {
			symbolImg = appkit.NSImageFromID(largeSymbol)
		}

		imageView := appkit.NewImageViewWithImage(&symbolImg)
		imageView.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: iconLeft, Y: (winH - iconSize) / 2},
			Size:   corefoundation.CGSize{Width: iconSize, Height: iconSize},
		})
		secondaryLabel := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSColor")), objc.Sel("secondaryLabelColor"),
		)
		objc.Send[objc.ID](imageView.ID, objc.Sel("setContentTintColor:"), secondaryLabel)

		// Apply repeating pulse symbol effect.
		pulseEffect := symbols.GetNSSymbolPulseEffectClass().Effect()
		if pulseEffect.ID != 0 {
			repeatBehavior := symbols.GetNSSymbolEffectOptionsRepeatBehaviorClass().BehaviorContinuous()
			opts := symbols.GetNSSymbolEffectOptionsClass().Options()
			repeatingOpts := opts.OptionsWithRepeatBehaviorWithBehavior(&repeatBehavior)
			objc.Send[objc.ID](imageView.ID,
				objc.Sel("addSymbolEffect:options:animated:"),
				pulseEffect.ID, repeatingOpts.(symbols.NSSymbolEffectOptions).ID, true,
			)
		}

		addSubview(contentView, imageView.NSView)
	}

	// Status label to the right of the icon.
	label := appkit.NewTextFieldLabelWithString("Preparing...")
	fontClass := appkit.GetNSFontClass()
	font := fontClass.SystemFontOfSize(13)
	label.SetFont(font)
	label.SetAlignment(appkit.NSTextAlignmentLeft)
	objc.Send[objc.ID](label.ID, objc.Sel("setBezeled:"), false)
	objc.Send[objc.ID](label.ID, objc.Sel("setDrawsBackground:"), false)
	objc.Send[objc.ID](label.ID, objc.Sel("setEditable:"), false)
	objc.Send[objc.ID](label.ID, objc.Sel("setSelectable:"), false)
	label.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: textLeft, Y: 60},
		Size:   corefoundation.CGSize{Width: textWidth, Height: 20},
	})
	addSubview(contentView, label.NSView)

	// Progress bar below the label, to the right of the icon.
	progressBarID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSProgressIndicator")),
		objc.Sel("alloc"),
	)
	progressBarID = objc.Send[objc.ID](progressBarID, objc.Sel("initWithFrame:"),
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: textLeft, Y: 30},
			Size:   corefoundation.CGSize{Width: textWidth, Height: 20},
		},
	)
	objc.Send[objc.ID](progressBarID, objc.Sel("setStyle:"), int64(0)) // NSProgressIndicatorStyleBar
	objc.Send[objc.ID](progressBarID, objc.Sel("setIndeterminate:"), true)
	objc.Send[objc.ID](progressBarID, objc.Sel("setMinValue:"), 0.0)
	objc.Send[objc.ID](progressBarID, objc.Sel("setMaxValue:"), 100.0)
	objc.Send[objc.ID](progressBarID, objc.Sel("startAnimation:"), objc.ID(0))

	barView := appkit.NSViewFromID(progressBarID)
	addSubview(contentView, barView)

	window.SetContentView(contentView)
	return window, label.ID, progressBarID
}

// ipswLooksComplete returns true if the file at path exists and looks like a
// complete IPSW archive. IPSW files are zip archives, so we check for the
// end-of-central-directory (EOCD) signature near the end of the file. A
// partial download will not have this signature.
func ipswLooksComplete(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() < 1*1024*1024*1024 { // must be at least 1 GB
		return false
	}

	// The EOCD record is at most 65557 bytes from the end of the file
	// (22 byte minimum EOCD + 65535 byte max comment). Read the last 256 bytes
	// which covers the common case (no zip comment).
	const tailSize = 256
	offset := info.Size() - tailSize
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, tailSize)
	n, err := f.ReadAt(buf, offset)
	if err != nil && n == 0 {
		return false
	}
	buf = buf[:n]

	// Search for EOCD signature: 0x50 0x4b 0x05 0x06
	for i := len(buf) - 4; i >= 0; i-- {
		if buf[i] == 0x50 && buf[i+1] == 0x4b && buf[i+2] == 0x05 && buf[i+3] == 0x06 {
			return true
		}
	}
	return false
}

// resolveOrDownloadIPSW finds a cached IPSW or downloads one.
func resolveOrDownloadIPSW(ctx context.Context) (string, error) {
	restoreImagePath := ipswPath
	if restoreImagePath == "" {
		cacheIPSW := filepath.Join(GetCacheDir(), "RestoreImage.ipsw")
		if ipswLooksComplete(cacheIPSW) {
			restoreImagePath = cacheIPSW
		} else {
			homeDir, _ := os.UserHomeDir()
			legacyCacheIPSW := filepath.Join(homeDir, ".cache", "vz", "restore.ipsw")
			if ipswLooksComplete(legacyCacheIPSW) {
				restoreImagePath = legacyCacheIPSW
			} else {
				os.MkdirAll(GetCacheDir(), 0755)
				restoreImagePath = cacheIPSW
				fmt.Println("No cached restore image found, downloading...")
				fmt.Println()
				if err := downloadRestoreImageVZ(ctx, restoreImagePath); err != nil {
					return "", fmt.Errorf("download restore image: %w", err)
				}
				fmt.Println()
			}
		}
	}
	return resolvePath(restoreImagePath), nil
}

// progressFunc receives status text and a percentage (0-100, or -1 for indeterminate).
type progressFunc func(text string, pct float64)

// resolveOrDownloadIPSWWithProgress is like resolveOrDownloadIPSW but reports
// progress via a callback for GUI display. Terminal output is also printed.
func resolveOrDownloadIPSWWithProgress(ctx context.Context, progress progressFunc) (string, error) {
	restoreImagePath := ipswPath
	if restoreImagePath != "" {
		progress("Using specified IPSW...", -1)
		return resolvePath(restoreImagePath), nil
	}

	cacheIPSW := filepath.Join(GetCacheDir(), "RestoreImage.ipsw")
	if ipswLooksComplete(cacheIPSW) {
		progress("Found cached restore image", 100)
		fmt.Printf("  Using cached IPSW: %s\n", cacheIPSW)
		return resolvePath(cacheIPSW), nil
	}

	homeDir, _ := os.UserHomeDir()
	legacyCacheIPSW := filepath.Join(homeDir, ".cache", "vz", "restore.ipsw")
	if ipswLooksComplete(legacyCacheIPSW) {
		progress("Found cached restore image", 100)
		fmt.Printf("  Using cached IPSW: %s\n", legacyCacheIPSW)
		return resolvePath(legacyCacheIPSW), nil
	}

	// No cached image — need to download.
	os.MkdirAll(GetCacheDir(), 0755)
	restoreImagePath = cacheIPSW

	progress("Fetching restore image URL from Apple...", -1)
	fmt.Println("No cached restore image found, downloading...")
	fmt.Println()

	if err := downloadRestoreImageVZWithProgress(ctx, restoreImagePath, progress); err != nil {
		return "", fmt.Errorf("download restore image: %w", err)
	}
	fmt.Println()
	return resolvePath(restoreImagePath), nil
}

// prepareInstaller loads the IPSW, configures the VM, and creates the installer.
func prepareInstaller(ctx context.Context, restoreImagePath string) (*macOSInstaller, error) {
	if verbose {
		fmt.Printf("Using restore image: %s\n", restoreImagePath)
	}

	fmt.Println("Loading restore image...")
	restoreImage, err := loadMacOSRestoreImageFromPath(restoreImagePath)
	if err != nil {
		return nil, fmt.Errorf("load restore image: %w", err)
	}

	configReqs := getMostFeaturefulSupportedConfiguration(restoreImage)
	if configReqs.ID == 0 {
		return nil, fmt.Errorf("no supported configuration for this host")
	}

	fmt.Println("Configuring virtual machine...")
	config, err := setupVMConfigurationWithRequirements(ctx, &configReqs)
	if err != nil {
		return nil, fmt.Errorf("setup configuration: %w", err)
	}

	vm, err := newVirtualMachine(config)
	if err != nil {
		return nil, fmt.Errorf("create VM: %w", err)
	}

	installer, err := newMacOSInstaller(vm, restoreImagePath)
	if err != nil {
		return nil, fmt.Errorf("create installer: %w", err)
	}

	if bootArgs != "" {
		bootArgsPath := filepath.Join(vmDir, "boot-args.txt")
		if err := os.WriteFile(bootArgsPath, []byte(bootArgs+"\n"), 0644); err != nil {
			fmt.Printf("warning: could not save boot-args: %v\n", err)
		}
	}

	return installer, nil
}

// loadMacOSRestoreImageFromPath loads a restore image using native bindings.
func loadMacOSRestoreImageFromPath(imagePath string) (vz.VZMacOSRestoreImage, error) {
	if _, err := os.Stat(imagePath); err != nil {
		return vz.VZMacOSRestoreImage{}, err
	}

	var restoreImage vz.VZMacOSRestoreImage
	var loadErr error
	done := make(chan struct{})

	// Create file URL from path
	fileURL := foundation.NewURLFileURLWithPath(imagePath)
	fileURL.Retain()

	vzlog("loadMacOSRestoreImageFromPath: calling loadFileURL %s", imagePath)

	// Use generated completion handler binding
	vz.GetVZMacOSRestoreImageClass().LoadFileURLCompletionHandler(fileURL, func(img *vz.VZMacOSRestoreImage, err error) {
		vzlog("loadMacOSRestoreImageFromPath: completion handler called img=%v err=%v", img, err)
		if err != nil {
			loadErr = err
		}
		if img != nil && img.ID != 0 {
			img.Retain()
			restoreImage = *img
		}
		close(done)
	})

	// Wait for completion. If NSApplication.Run() is active (GUI mode), the
	// main run loop is already being pumped — just sleep. Otherwise, pump it
	// ourselves via runRunLoopOnce().
	timeout := time.After(60 * time.Second)
	for {
		select {
		case <-done:
			if loadErr != nil {
				return vz.VZMacOSRestoreImage{}, loadErr
			}
			return restoreImage, nil
		case <-timeout:
			return vz.VZMacOSRestoreImage{}, fmt.Errorf("timeout loading restore image")
		default:
			// In non-GUI mode, pump the run loop from this goroutine.
			// In GUI mode, the main thread's manual event loop handles
			// run loop pumping — just sleep and wait.
			if !guiMode {
				vzkit.RunRunLoopOnce()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// getMostFeaturefulSupportedConfiguration gets the best config from restore image using native bindings.
func getMostFeaturefulSupportedConfiguration(img vz.VZMacOSRestoreImage) vz.VZMacOSConfigurationRequirements {
	ireqs := img.MostFeaturefulSupportedConfiguration()
	reqs := vz.VZMacOSConfigurationRequirementsFromID(ireqs.GetID())
	if reqs.ID != 0 {
		reqs.Retain()
	}

	// Debug: print the minimum requirements from the IPSW
	if reqs.ID != 0 {
		minCPU := reqs.MinimumSupportedCPUCount()
		minMem := reqs.MinimumSupportedMemorySize()
		vzlog("IPSW requirements: minCPU=%d, minMemory=%d bytes (%.1f GB)",
			minCPU, minMem, float64(minMem)/(1024*1024*1024))
	}
	return reqs
}

// setupVMConfigurationWithRequirements creates VM config using IPSW requirements.
// Mirrors Code-Hex/vz's setupVirtualMachineWithMacOSConfigurationRequirements.
func setupVMConfigurationWithRequirements(ctx context.Context, reqs *vz.VZMacOSConfigurationRequirements) (vz.VZVirtualMachineConfiguration, error) {
	// Create platform configuration
	platformConfig, err := createMacInstallerPlatformConfiguration(reqs)
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("create platform config: %w", err)
	}

	// Create main VM configuration
	return setupVMConfiguration(ctx, platformConfig, uint(reqs.MinimumSupportedCPUCount()), reqs.MinimumSupportedMemorySize())
}

// createMacInstallerPlatformConfiguration creates platform config for installation.
// Mirrors Code-Hex/vz's createMacInstallerPlatformConfiguration.
func createMacInstallerPlatformConfiguration(reqs *vz.VZMacOSConfigurationRequirements) (vz.VZMacPlatformConfiguration, error) {
	// Get hardware model from requirements
	hwModel := getHardwareModel(reqs)
	if hwModel.ID == 0 {
		return vz.VZMacPlatformConfiguration{}, fmt.Errorf("failed to get hardware model")
	}
	if !hwModel.Supported() {
		return vz.VZMacPlatformConfiguration{}, fmt.Errorf("hardware model not supported on this host")
	}

	// Save hardware model data for future runs
	hwModelPath := filepath.Join(vmDir, "hw.model")
	if err := saveDataRepresentation(hwModel.ID, hwModelPath); err != nil {
		fmt.Printf("warning: could not save hardware model: %v\n", err)
	}

	// Create new machine identifier
	machineID := vz.NewVZMacMachineIdentifier()
	if machineID.ID == 0 {
		return vz.VZMacPlatformConfiguration{}, fmt.Errorf("failed to create machine identifier")
	}

	// Save machine identifier data for future runs
	machineIDPath := filepath.Join(vmDir, "machine.id")
	if err := saveDataRepresentation(machineID.ID, machineIDPath); err != nil {
		fmt.Printf("warning: could not save machine identifier: %v\n", err)
	}

	// Create auxiliary storage with hardware model (key difference from runtime)
	auxStoragePath := filepath.Join(vmDir, "aux.img")
	auxURL := foundation.NewURLFileURLWithPath(auxStoragePath)
	auxURL.Retain()

	auxStorage, err := vz.NewMacAuxiliaryStorageCreatingStorageAtURLHardwareModelOptionsError(
		auxURL, hwModel, vz.VZMacAuxiliaryStorageInitializationOptionAllowOverwrite)
	if err != nil {
		return vz.VZMacPlatformConfiguration{}, fmt.Errorf("failed to create auxiliary storage: %w", err)
	}
	auxStorage.Retain() // Keep alive for VM

	// Build platform configuration
	platformConfig := vz.NewVZMacPlatformConfiguration()
	platformConfig.SetHardwareModel(hwModel)
	platformConfig.SetMachineIdentifier(&machineID)
	platformConfig.SetAuxiliaryStorage(&auxStorage)

	return platformConfig, nil
}

// getHardwareModel extracts hardware model from configuration requirements using native bindings.
func getHardwareModel(reqs *vz.VZMacOSConfigurationRequirements) vz.VZMacHardwareModel {
	ihwModel := reqs.HardwareModel()
	hwModel := vz.VZMacHardwareModelFromID(ihwModel.GetID())
	if hwModel.ID != 0 {
		hwModel.Retain()
	}
	return hwModel
}

// saveDataRepresentation saves an object's dataRepresentation to a file.
// NOTE: This uses objc.Send because DataRepresentation() may not be available on all types.
func saveDataRepresentation(objID objc.ID, path string) error {
	dataID := objc.Send[objc.ID](objID, objc.Sel("dataRepresentation"))
	if dataID == 0 {
		return fmt.Errorf("no data representation")
	}
	data := foundation.NSDataFromID(dataID)
	length := data.Length()
	if length == 0 {
		return fmt.Errorf("empty data")
	}
	ptr := data.Bytes()
	bytes := unsafe.Slice((*byte)(ptr), length)
	return os.WriteFile(path, bytes, 0644)
}

// setupVMConfiguration creates the full VM configuration.
// Mirrors Code-Hex/vz's setupVMConfiguration.
func setupVMConfiguration(ctx context.Context, platformConfig vz.VZMacPlatformConfiguration, minCPU uint, minMem uint64) (vz.VZVirtualMachineConfiguration, error) {
	// Create boot loader
	bootloader := vz.NewVZMacOSBootLoader()
	if bootloader.ID == 0 {
		return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("failed to create bootloader")
	}

	// Create main configuration
	config := vz.NewVZVirtualMachineConfiguration()
	config.SetBootLoader(&bootloader.VZBootLoader)
	config.SetCPUCount(computeInstallCPUCount(minCPU))
	config.SetMemorySize(computeInstallMemorySize(minMem))
	config.SetPlatform(&platformConfig.VZPlatformConfiguration)

	// Graphics
	graphicsConfig, err := createGraphicsDeviceConfiguration()
	if err != nil {
		return config, fmt.Errorf("graphics config: %w", err)
	}
	setGraphicsDevices(config, graphicsConfig)

	// Storage (disk)
	diskPath := filepath.Join(vmDir, "disk.img")
	blockConfig, err := createBlockDeviceConfiguration(ctx, diskPath)
	if err != nil {
		return config, fmt.Errorf("block device config: %w", err)
	}
	setStorageDevices(config, blockConfig)

	// Network
	networkConfig, err := createNetworkDeviceConfiguration()
	if err != nil {
		return config, fmt.Errorf("network config: %w", err)
	}
	setNetworkDevices(config, networkConfig)

	// Pointing devices (USB + trackpad if available)
	pointingDevices := []vz.IVZPointingDeviceConfiguration{
		vz.NewVZUSBScreenCoordinatePointingDeviceConfiguration(),
	}
	// Add MacTrackpad if available
	if trackpad := vz.NewVZMacTrackpadConfiguration(); trackpad.GetID() != 0 {
		pointingDevices = append(pointingDevices, trackpad)
	}
	setPointingDevices(config, pointingDevices)

	// Keyboard (try Mac keyboard, fallback to USB)
	keyboardConfig := createKeyboardConfiguration()
	setKeyboards(config, keyboardConfig)

	// Audio
	audioConfig, err := createAudioDeviceConfiguration()
	if err != nil {
		fmt.Printf("warning: audio config: %v\n", err)
	} else {
		setAudioDevices(config, audioConfig)
	}

	// Entropy
	entropyConfig := vz.NewVZVirtioEntropyDeviceConfiguration()
	setEntropyDevices(config, entropyConfig)

	// Volume mounts (VirtioFS) - also configure during install for first-boot provisioning
	effectiveVolumes := getEffectiveVolumes()
	if len(effectiveVolumes) > 0 {
		volumeConfigs, err := createVolumeConfigs(effectiveVolumes)
		if err != nil {
			fmt.Printf("warning: volume config: %v\n", err)
		} else if len(volumeConfigs) > 0 {
			setDirectorySharingDevicesMulti(config, volumeConfigs)
		}
	}

	// Validate
	if _, err := config.ValidateWithError(); err != nil {
		return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("validation failed: %w", err)
	}

	return config, nil
}

func clampInstallCPUCount(requested, frameworkMin, frameworkMax, restoreMin uint) uint {
	minCPU := frameworkMin
	if restoreMin > minCPU {
		minCPU = restoreMin
	}
	if requested < minCPU {
		requested = minCPU
	}
	if requested > frameworkMax {
		requested = frameworkMax
	}
	return requested
}

// computeInstallCPUCount returns an install-time CPU count that respects both
// framework-wide limits and the restore image's minimum requirements.
func computeInstallCPUCount(minRequired uint) uint {
	configClass := vz.GetVZVirtualMachineConfigurationClass()
	frameworkMin := uint(configClass.MinimumAllowedCPUCount())
	frameworkMax := uint(configClass.MaximumAllowedCPUCount())
	virtualCPUCount := clampInstallCPUCount(cpuCount, frameworkMin, frameworkMax, minRequired)
	vzlog("computeInstallCPUCount: requested=%d, frameworkMin=%d, restoreMin=%d, max=%d, using=%d",
		cpuCount, frameworkMin, minRequired, frameworkMax, virtualCPUCount)
	return virtualCPUCount
}

func clampInstallMemorySize(requested, frameworkMin, frameworkMax, restoreMin uint64) uint64 {
	minMem := frameworkMin
	if restoreMin > minMem {
		minMem = restoreMin
	}
	if requested < minMem {
		requested = minMem
	}
	if requested > frameworkMax {
		requested = frameworkMax
	}
	return requested
}

// computeInstallMemorySize returns an install-time memory size that respects
// both framework-wide limits and the restore image's minimum requirements.
func computeInstallMemorySize(minRequired uint64) uint64 {
	configClass := vz.GetVZVirtualMachineConfigurationClass()
	frameworkMin := configClass.MinimumAllowedMemorySize()
	frameworkMax := configClass.MaximumAllowedMemorySize()

	requested := memoryGB * 1024 * 1024 * 1024
	memSize := clampInstallMemorySize(requested, frameworkMin, frameworkMax, minRequired)
	vzlog("computeInstallMemorySize: requested=%d GB, frameworkMin=%d (%.1f GB), restoreMin=%d (%.1f GB), max=%d (%.1f GB), using=%d (%.1f GB)",
		memoryGB, frameworkMin, float64(frameworkMin)/(1024*1024*1024), minRequired, float64(minRequired)/(1024*1024*1024), frameworkMax, float64(frameworkMax)/(1024*1024*1024),
		memSize, float64(memSize)/(1024*1024*1024))
	return memSize
}

// createGraphicsDeviceConfiguration creates graphics configuration.
func createGraphicsDeviceConfiguration() (vz.VZMacGraphicsDeviceConfiguration, error) {
	graphicsConfig := vz.NewVZMacGraphicsDeviceConfiguration()
	if graphicsConfig.ID == 0 {
		return graphicsConfig, fmt.Errorf("failed to create graphics config")
	}

	// Use the full constructor - the parameterless NewMacGraphicsDisplayConfiguration()
	// doesn't create a valid display config
	displayConfig := vz.NewMacGraphicsDisplayConfigurationWithWidthInPixelsHeightInPixelsPixelsPerInch(
		1920, 1200, 80)
	if displayConfig.ID == 0 {
		return graphicsConfig, fmt.Errorf("failed to create display config")
	}

	setDisplays(graphicsConfig, displayConfig)
	return graphicsConfig, nil
}

// createBlockDeviceConfiguration creates disk storage configuration.
func createBlockDeviceConfiguration(ctx context.Context, diskPath string) (vz.VZVirtioBlockDeviceConfiguration, error) {
	// Create disk if needed
	if err := createDiskImage(diskPath, diskSizeGB); err != nil {
		if !os.IsExist(err) {
			return vz.VZVirtioBlockDeviceConfiguration{}, err
		}
	}

	diskURL := foundation.NewURLFileURLWithPath(diskPath)
	diskURL.Retain() // Create disk attachment
	diskAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(diskURL, false)
	if err != nil {
		return vz.VZVirtioBlockDeviceConfiguration{}, fmt.Errorf("failed to create disk attachment: %w", err)
	}
	diskAttachment.Retain()

	// Create block device custom config
	storageConfig := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&diskAttachment.VZStorageDeviceAttachment)
	storageConfig.Retain()
	return storageConfig, nil
}

// createNetworkDeviceConfiguration creates network configuration.
func createNetworkDeviceConfiguration() (vz.VZVirtioNetworkDeviceConfiguration, error) {
	natAttachment := vz.NewVZNATNetworkDeviceAttachment()
	if natAttachment.ID == 0 {
		return vz.VZVirtioNetworkDeviceConfiguration{}, fmt.Errorf("failed to create NAT attachment")
	}

	networkConfig := vz.NewVZVirtioNetworkDeviceConfiguration()
	if networkConfig.ID == 0 {
		return networkConfig, fmt.Errorf("failed to create network config")
	}

	networkConfig.SetAttachment(&natAttachment.VZNetworkDeviceAttachment)
	return networkConfig, nil
}

// createKeyboardConfiguration creates keyboard configuration.
// Tries Mac keyboard first, falls back to USB.
func createKeyboardConfiguration() vz.IVZKeyboardConfiguration {
	// Try VZMacKeyboardConfiguration first when available
	if macKeyboard := vz.NewVZMacKeyboardConfiguration(); macKeyboard.GetID() != 0 {
		return macKeyboard
	}
	// Fall back to USB keyboard
	return vz.NewVZUSBKeyboardConfiguration()
}

// createAudioDeviceConfiguration creates audio configuration with host streams.
// This matches Code-Hex/vz's createAudioDeviceConfiguration which sets up
// input (microphone) and output (speaker) streams connected to the host.
func createAudioDeviceConfiguration() (vz.VZVirtioSoundDeviceConfiguration, error) {
	audioConfig := vz.NewVZVirtioSoundDeviceConfiguration()
	if audioConfig.ID == 0 {
		return audioConfig, fmt.Errorf("failed to create audio config")
	}

	// TESTING: Skip audio streams to verify they're needed for installation
	if os.Getenv("VZ_SKIP_AUDIO_STREAMS") != "" {
		vzlog("createAudioDeviceConfiguration: SKIPPING audio streams (VZ_SKIP_AUDIO_STREAMS set)")
		return audioConfig, nil
	}

	// Create input stream with host source (like Code-Hex/vz)
	// VZVirtioSoundDeviceHostInputStreamConfiguration = VZVirtioSoundDeviceInputStreamConfiguration + VZHostAudioInputStreamSource
	inputStream := vz.NewVZVirtioSoundDeviceInputStreamConfiguration()
	if inputStream.ID == 0 {
		return audioConfig, fmt.Errorf("failed to create input stream")
	}
	hostInputSource := vz.NewVZHostAudioInputStreamSource()
	if hostInputSource.ID == 0 {
		return audioConfig, fmt.Errorf("failed to create host input source")
	}
	inputStream.SetSource(&hostInputSource.VZAudioInputStreamSource)

	// Create output stream with host sink (like Code-Hex/vz)
	// VZVirtioSoundDeviceHostOutputStreamConfiguration = VZVirtioSoundDeviceOutputStreamConfiguration + VZHostAudioOutputStreamSink
	outputStream := vz.NewVZVirtioSoundDeviceOutputStreamConfiguration()
	if outputStream.ID == 0 {
		return audioConfig, fmt.Errorf("failed to create output stream")
	}
	hostOutputSink := vz.NewVZHostAudioOutputStreamSink()
	if hostOutputSink.ID == 0 {
		return audioConfig, fmt.Errorf("failed to create host output sink")
	}
	outputStream.SetSink(&hostOutputSink.VZAudioOutputStreamSink)

	// Use generated SetStreams with FromID conversion to base type
	audioConfig.SetStreams([]vz.VZVirtioSoundDeviceStreamConfiguration{
		vz.VZVirtioSoundDeviceStreamConfigurationFromID(inputStream.ID),
		vz.VZVirtioSoundDeviceStreamConfigurationFromID(outputStream.ID),
	})

	vzlog("createAudioDeviceConfiguration: configured with host input/output streams")
	return audioConfig, nil
}

// newVirtualMachine creates a new VM with its own dispatch queue.
// Mirrors Code-Hex/vz's NewVirtualMachine.
func newVirtualMachine(config vz.VZVirtualMachineConfiguration) (*virtualMachine, error) {
	queue := dispatch.QueueCreate("com.appledocs.vz.vm")

	vzVM := vz.NewVirtualMachineWithConfigurationQueue(&config, queue)
	if vzVM.ID == 0 {
		return nil, fmt.Errorf("failed to create VM")
	}
	vzVM.Retain()

	// Get initial state
	initialState := vz.VZVirtualMachineState(vzVM.State())
	vzlog("newVirtualMachine: created VM=%#x queue=%#x initialState=%d", vzVM.ID, queue.Handle(), initialState)

	vm := &virtualMachine{
		vm:           vzVM,
		queue:        queue,
		stateChanged: make(chan vz.VZVirtualMachineState, 10),
		currentState: initialState,
		done:         make(chan struct{}),
	}

	// Start state monitoring goroutine
	go vm.monitorState()

	return vm, nil
}

// vmStateName returns a human-readable name for a VM state.
func vmStateName(state vz.VZVirtualMachineState) string {
	switch state {
	case vz.VZVirtualMachineStateStopped:
		return "Stopped"
	case vz.VZVirtualMachineStateRunning:
		return "Running"
	case vz.VZVirtualMachineStatePaused:
		return "Paused"
	case vz.VZVirtualMachineStateError:
		return "Error"
	case vz.VZVirtualMachineStateStarting:
		return "Starting"
	case vz.VZVirtualMachineStatePausing:
		return "Pausing"
	case vz.VZVirtualMachineStateResuming:
		return "Resuming"
	case vz.VZVirtualMachineStateStopping:
		return "Stopping"
	case vz.VZVirtualMachineStateSaving:
		return "Saving"
	case vz.VZVirtualMachineStateRestoring:
		return "Restoring"
	default:
		return fmt.Sprintf("Unknown(%d)", state)
	}
}

// monitorState polls VM state and sends updates to stateChanged channel.
// Note: In Code-Hex/vz this is done via KVO, but that requires CGO.
// For purego, we poll the state periodically.
func (vm *virtualMachine) monitorState() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-vm.done:
			return
		case <-ticker.C:
		}

		if vm.vm.ID == 0 {
			return
		}

		newState := vz.VZVirtualMachineState(vm.vm.State())
		vm.stateLock.Lock()
		oldState := vm.currentState
		if newState != oldState {
			vm.currentState = newState
			vzlog("monitorState: state changed %s -> %s", vmStateName(oldState), vmStateName(newState))
			select {
			case vm.stateChanged <- newState:
			default:
				// Channel full, skip
			}
		}
		vm.stateLock.Unlock()
	}
}

// State returns the current VM state.
func (vm *virtualMachine) State() vz.VZVirtualMachineState {
	vm.stateLock.Lock()
	defer vm.stateLock.Unlock()
	return vm.currentState
}

// stopMonitor signals the monitorState goroutine to exit.
func (vm *virtualMachine) stopMonitor() {
	select {
	case <-vm.done:
		// already closed
	default:
		close(vm.done)
	}
}

// virtualMachine wraps VZVirtualMachine with its dispatch queue.
// Mirrors Code-Hex/vz's VirtualMachine struct.
type virtualMachine struct {
	vm           vz.VZVirtualMachine
	queue        dispatch.Queue
	stateChanged chan vz.VZVirtualMachineState
	stateLock    sync.Mutex
	currentState vz.VZVirtualMachineState
	observer     objc.ID       // For KVO observation
	done         chan struct{} // closed to stop monitorState goroutine
}

// newMacOSInstaller creates a macOS installer.
// Mirrors Code-Hex/vz's NewMacOSInstaller.
func newMacOSInstaller(vm *virtualMachine, restoreImagePath string) (*macOSInstaller, error) {
	if _, err := os.Stat(restoreImagePath); err != nil {
		return nil, err
	}

	// Get disk URL
	diskURL := foundation.NewURLFileURLWithPath(restoreImagePath)
	diskURL.Retain()

	// Create installer using the VM's dispatch queue
	vzlog("newMacOSInstaller: creating VZMacOSInstaller for VM=%#x queue=%#x", vm.vm.ID, vm.queue.Handle())

	var installer vz.VZMacOSInstaller
	DispatchSync(uintptr(vm.queue.Handle()), func() {
		// Use native binding
		installer = vz.NewMacOSInstallerWithVirtualMachineRestoreImageURL(&vm.vm, diskURL)
		vzlog("newMacOSInstaller: created installer=%#x", installer.ID)
	})

	if installer.ID == 0 {
		return nil, fmt.Errorf("failed to create installer")
	}

	// Retain installer (counteract autorelease in binding)
	installer.Retain()

	return &macOSInstaller{
		installer: installer,
		vm:        vm,
	}, nil
}

// macOSInstaller wraps VZMacOSInstaller.
type macOSInstaller struct {
	installer vz.VZMacOSInstaller
	vm        *virtualMachine
}

// printProgress displays a compact progress bar.
func printProgress(percent float64) {
	const barWidth = 30
	filled := int(percent / 100 * barWidth)
	if filled > barWidth {
		filled = barWidth
	}
	bar := make([]byte, barWidth)
	for i := range bar {
		if i < filled {
			bar[i] = '='
		} else if i == filled {
			bar[i] = '>'
		} else {
			bar[i] = ' '
		}
	}
	// Clear line and print progress
	fmt.Printf("\r\033[K[%s] %5.1f%%", string(bar), percent)
}

// runInstallation runs the installation with progress monitoring.
// This version avoids using purego blocks for the completion handler entirely.
// Instead, it passes nil and polls NSProgress.isFinished to detect completion.
// This avoids XPC callback issues where purego blocks may interfere with
// MobileDevice XPC services during the DFU->RestoreOS state transition.
func runInstallation(ctx context.Context, installer *macOSInstaller) error {
	defer installer.vm.stopMonitor()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Create completion handler to signal when installation finishes
	doneCh := make(chan error, 1)

	// CRITICAL: Must call installWithCompletionHandler on the VM's dispatch queue
	// Code-Hex/vz does this via dispatch_sync(vmQueue, ^{ [installer installWithCompletionHandler:...] })
	// This ensures proper XPC service communication for the installation process.
	vzlog("runInstallation: dispatching installWithCompletionHandler to VM queue")
	DispatchSync(uintptr(installer.vm.queue.Handle()), func() {
		vzlog("runInstallation: [on VM queue] calling installWithCompletionHandler")
		// Use native binding with typed handler
		installer.installer.InstallWithCompletionHandler(func(err error) {
			if err != nil {
				vzlog("runInstallation: completion handler called err=%v", err)
				doneCh <- err
			} else {
				vzlog("runInstallation: completion handler called err=nil")
				doneCh <- nil
			}
		})
		vzlog("runInstallation: [on VM queue] installWithCompletionHandler returned (async)")
	})
	vzlog("runInstallation: dispatch_sync returned, installation initiated")

	// Monitor progress with callback-based completion
	progress := installer.installer.Progress()
	if progress.GetID() == 0 {
		return fmt.Errorf("installer has no progress object")
	}

	lastPercent := -1.0
	lastStateLog := time.Time{}

	vzlog("runInstallation: entering progress monitoring loop")

	for {
		select {
		case <-ctx.Done():
			// Cancel installation
			vzlog("runInstallation: context cancelled, cancelling installation")
			if progress.Cancellable() {
				progress.Cancel()
			}
			return ctx.Err()

		case err := <-doneCh:
			// Installation completed (success or failure)
			fmt.Println()
			if err != nil {
				vzlog("runInstallation: completion handler returned error: %v", err)
				printDetailedInstallError(err)
				return fmt.Errorf("installation failed: %w", err)
			}
			vzlog("runInstallation: installation complete!")
			fmt.Println("=== Installation Complete ===")
			fmt.Println()

			stopVMAndInject(installer.vm)

			fmt.Println("You can now run the VM with: ./vz-macos run")
			if provisionUser == "" {
				fmt.Println()
				fmt.Println("For auto-provisioning (automatic user creation), add flags:")
				fmt.Println("  ./vz-macos install -provision-user myuser -provision-password mypass")
				fmt.Println()
				fmt.Println("Or provision after installation:")
				fmt.Printf("  ./vz-macos provision -user myuser -password mypass -skip-setup-assistant\n")
			}
			fmt.Println()
			return nil

		default:
			// Pump run loop to process XPC messages and framework callbacks
			vzkit.RunRunLoopAggressively()

			// Update progress display
			currentPercent := progress.FractionCompleted() * 100
			if currentPercent-lastPercent >= 1.0 || lastPercent < 0 {
				printProgress(currentPercent)
				lastPercent = currentPercent
			}

			// Log state periodically for debugging (only when debug enabled, doesn't break progress bar)
			if vzDebugInstall && time.Since(lastStateLog) > 5*time.Second {
				vmState := installer.vm.State()
				// Print on new line then reprint progress to keep display clean
				fmt.Printf("\n[DEBUG] progress=%.1f%% vmState=%d\n", currentPercent, vmState)
				lastStateLog = time.Now()
				// Reprint progress bar
				printProgress(currentPercent)
			}

			time.Sleep(100 * time.Millisecond)
		}
	}
}

// downloadRestoreImageVZ downloads the restore image using vz pattern.
func downloadRestoreImageVZ(ctx context.Context, destPath string) error {
	// First get the URL from Apple's servers
	var restoreImage vz.VZMacOSRestoreImage
	var fetchErr error
	done := make(chan struct{})

	start := time.Now()
	vz.GetVZMacOSRestoreImageClass().FetchLatestSupportedWithCompletionHandler(func(img *vz.VZMacOSRestoreImage, err error) {
		if err != nil {
			fetchErr = err
		}
		if img != nil && img.ID != 0 {
			img.Retain()
			restoreImage = *img
		}
		close(done)
	})

	// Wait with spinner (matches fetchLatestRestoreImageObject style)
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0
	for {
		select {
		case <-done:
			if fetchErr != nil {
				fmt.Printf("\r\033[K")
				return hintEntitlements(fetchErr)
			}
			fmt.Printf("\r\033[K")
		default:
			if time.Since(start) > 30*time.Second {
				fmt.Printf("\r\033[K")
				return fmt.Errorf("timeout fetching restore image info")
			}
			vzkit.RunRunLoopOnce()
			elapsed := time.Since(start).Truncate(100 * time.Millisecond)
			fmt.Printf("\r  %s Fetching restore image URL from Apple... %v", spinner[i%len(spinner)], elapsed)
			i++
			time.Sleep(100 * time.Millisecond)
			continue
		}
		break
	}

	if restoreImage.ID == 0 {
		return fmt.Errorf("no restore image returned")
	}

	// Get download URL and build version
	downloadURL := restoreImage.URL().AbsoluteString()
	buildVersion := restoreImage.BuildVersion()
	if buildVersion != "" {
		fmt.Printf("  Restore image: macOS (build %s)\n", buildVersion)
	}
	fmt.Printf("  Downloading: %s\n", downloadURL)
	fmt.Printf("  Saving to:   %s\n", destPath)
	fmt.Println()

	// Download using curl (resumable, has its own progress display)
	return downloadIPSWCurl(downloadURL, destPath)
}

// Create completion handler

// Start installation on VM's dispatch queue

// Get progress object for monitoring

// Set up GUI

// Create VM view

// Create window

// Ensure standard window chrome is visible

// Create "Starting installation..." overlay on top of the VM view

// Track installation completion

// Show window and make VM view first responder

// Monitor installation completion in a background goroutine.
// Sets installComplete/installErr; the NSTimer on the main thread
// polls these and calls app.Stop() when done.

// NSTimer polls progress and completion at ~2 Hz, applies UI changes
// on the main thread. Avoids DispatchAsync(GetMainDispatchQueue())
// purego callback GC stack corruption.

// Update progress.

// Check for completion.

// app.Run() drains both the GCD main queue and CFRunLoop.

// createInstallOverlay creates a dark overlay view with a "Starting installation..." label.
func createInstallOverlay(size corefoundation.CGSize) appkit.NSView {
	frame := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   size,
	}

	overlay := appkit.NewViewWithFrame(frame)
	objc.Send[objc.ID](overlay.ID, objc.Sel("setWantsLayer:"), true)

	// Dark semi-transparent background via CALayer
	layer := objc.Send[objc.ID](overlay.ID, objc.Sel("layer"))
	if layer != 0 {
		// [layer setBackgroundColor:CGColorCreateGenericRGB(0, 0, 0, 0.85)]
		bgColor := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSColor")),
			objc.Sel("colorWithWhite:alpha:"),
			0.1, 0.9,
		)
		cgColor := objc.Send[objc.ID](bgColor, objc.Sel("CGColor"))
		objc.Send[objc.ID](layer, objc.Sel("setBackgroundColor:"), cgColor)
	}

	// Create label — fill the overlay and center-align the text
	label := appkit.NewTextFieldLabelWithString("Starting installation...")
	fontClass := appkit.GetNSFontClass()
	font := fontClass.SystemFontOfSizeWeight(24, -0.4) // NSFontWeightLight
	label.SetFont(font)
	label.SetAlignment(appkit.NSTextAlignmentCenter)
	whiteColor := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSColor")),
		objc.Sel("whiteColor"),
	)
	objc.Send[objc.ID](label.ID, objc.Sel("setTextColor:"), whiteColor)
	objc.Send[objc.ID](label.ID, objc.Sel("setBezeled:"), false)
	objc.Send[objc.ID](label.ID, objc.Sel("setDrawsBackground:"), false)
	objc.Send[objc.ID](label.ID, objc.Sel("setEditable:"), false)
	// Position label centered vertically, spanning full width
	label.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{
			X: 0,
			Y: (size.Height - 40) / 2,
		},
		Size: corefoundation.CGSize{Width: size.Width, Height: 40},
	})

	addSubview(overlay, label.NSView)
	return overlay
}

// postDummyEvent posts an application-defined event to unblock app.Run()
// after app.Stop() has been called. Uses objc.Send directly to pass nil
// context (the generated binding crashes on nil *NSGraphicsContext).
func postDummyEvent(app appkit.NSApplication) {
	eventID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSEvent")),
		objc.Sel("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:"),
		nsEventTypeApplicationDefined,
		corefoundation.CGPoint{},
		appkit.NSEventModifierFlags(0),
		float64(0),
		int(0),
		objc.ID(0), // nil context
		int16(0),
		int(0),
		int(0),
	)
	event := appkit.NSEventFromID(eventID)
	app.PostEventAtStart(&event, true)
}
