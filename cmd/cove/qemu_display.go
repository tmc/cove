package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/cove/internal/rfb"
)

var qemuDisplayViewClassCounter atomic.Int64
var qemuDisplayMenuClassCounter atomic.Int64
var qemuDisplayToolbarClassCounter atomic.Int64

func runQEMUDisplayCommand(env commandEnv, name string, args []string) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	vm := fs.String("vm", "", "VM name")
	refresh := fs.Duration("refresh", time.Second, "frame refresh interval")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *vm == "" {
		fmt.Fprintln(env.Stderr, "qemu-display: -vm is required")
		return 2
	}
	dir, err := requireExistingVMDir("qemu-display", *vm)
	if err != nil {
		fmt.Fprintf(env.Stderr, "qemu-display: %v\n", err)
		return 1
	}
	if err := runWindowsQEMUDisplayWindow(context.Background(), dir, *vm, *refresh); err != nil {
		fmt.Fprintf(env.Stderr, "qemu-display: %v\n", err)
		return 1
	}
	return 0
}

func runWindowsQEMUDisplayWindow(ctx context.Context, vmDir, name string, refresh time.Duration) error {
	if refresh <= 0 {
		refresh = time.Second
	}
	if err := writeWindowsQEMUViewerPID(vmDir, os.Getpid()); err != nil {
		return err
	}
	defer os.Remove(windowsQEMUViewerPIDPath(vmDir))
	if err := writeWindowsQEMUViewerInputMode(vmDir, qemuDisplayInputMode()); err != nil {
		return err
	}
	defer os.Remove(windowsQEMUViewerInputModePath(vmDir))
	status := readWindowsQEMUCTLStatus(vmDir)
	if status.VNCEndpoint == "" {
		return fmt.Errorf("qemu vnc is not enabled; restart with -vnc :5901")
	}
	first, err := captureWindowsQEMUImage(vmDir)
	if err != nil {
		return fmt.Errorf("capture first qemu frame: %w", err)
	}
	rfbClient, err := rfb.Dial(ctx, status.VNCEndpoint)
	if err != nil {
		return fmt.Errorf("connect qemu display rfb: %w", err)
	}
	defer rfbClient.Close()

	app := ensureAppReady(appkit.NSApplicationActivationPolicyRegular)
	var closed atomic.Bool
	var view appkit.NSImageView
	var input *qemuDisplayInput
	var window appkit.NSWindow
	var windowDelegate appkit.NSWindowDelegateObject
	var frameAutosaveName appkit.NSWindowFrameAutosaveName
	var eventMonitors []objectivec.IObject
	var menuTarget objc.ID
	var toolbarDelegate objc.ID
	runOnUIThreadSync(func() {
		transformToForegroundApp()
		setAppIcon(&app)
		setDockBadgeLabel(app, name)
		rect := imageContentRect(first)
		view, input = newQEMUDisplayImageView(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{},
			Size:   rect.Size,
		}, rfbClient, first.Bounds().Size())
		view.SetImageScaling(appkit.NSImageScaleAxesIndependently)
		view.NSView.SetAutoresizingMask(appkit.NSViewWidthSizable | appkit.NSViewHeightSizable)
		view.SetImage(nsImageFromImage(first))

		window = appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			rect,
			appkit.NSWindowStyleMaskTitled|
				appkit.NSWindowStyleMaskClosable|
				appkit.NSWindowStyleMaskMiniaturizable|
				appkit.NSWindowStyleMaskResizable,
			appkit.NSBackingStoreBuffered,
			false,
		)
		window.SetStyleMask(
			appkit.NSWindowStyleMaskTitled |
				appkit.NSWindowStyleMaskClosable |
				appkit.NSWindowStyleMaskMiniaturizable |
				appkit.NSWindowStyleMaskResizable,
		)
		window.SetTitleVisibility(appkit.NSWindowTitleVisible)
		window.SetTitlebarAppearsTransparent(false)
		window.SetTitle("Windows VM — " + name)
		window.SetReleasedWhenClosed(false)
		window.SetContentView(view.NSView)
		window.SetAcceptsMouseMovedEvents(true)
		restoredFrame, autosaveName := configureWindowFramePersistenceForVM(window, name, vmDir, "windows-qemu")
		frameAutosaveName = autosaveName
		if !restoredFrame {
			window.Center()
		}
		windowDelegate = appkit.NewNSWindowDelegate(appkit.NSWindowDelegateConfig{
			ShouldClose: func(_ appkit.NSWindow) bool {
				closed.Store(true)
				return true
			},
		})
		window.SetDelegate(windowDelegate)
		menuTarget = newQEMUDisplayMenuTarget(vmDir, &closed, &window)
		setupQEMUDisplayMainMenu(menuTarget)
		toolbarDelegate = setupQEMUDisplayToolbar(window, menuTarget)
		if qemuDisplayLegacyMonitors() {
			eventMonitors = installQEMUDisplayGlobalMonitors(window, view.NSView, input)
		}
		window.MakeKeyAndOrderFront(nil)
		window.OrderFrontRegardless()
		window.MakeKeyWindow()
		window.MakeFirstResponder(view.NSResponder)
		window.DisplayIfNeeded()
		app.Unhide(nil)
		activateQEMUDisplayApp(app)
		objc.Send[objc.ID](window.ID, objc.Sel("retain"))
		if qemuDisplayDebug() {
			visible := objc.Send[bool](window.ID, objc.Sel("isVisible"))
			fmt.Fprintf(os.Stderr, "qemu-display: window id=%#x visible=%v windows=%d ordered=%d frame=%dx%d\n",
				uintptr(window.ID), visible, len(app.Windows()), len(app.OrderedWindows()),
				int(rect.Size.Width), int(rect.Size.Height))
		}
	})
	_ = windowDelegate
	_ = menuTarget
	_ = toolbarDelegate
	defer runOnUIThreadSync(func() {
		for _, monitor := range eventMonitors {
			appkit.GetNSEventClass().RemoveMonitor(monitor)
		}
		if window.ID != 0 {
			if frameAutosaveName != "" {
				saveWindowDisplayPlacementForDir(window, frameAutosaveName, vmDir)
				window.SaveFrameUsingName(frameAutosaveName)
			}
			window.OrderOut(nil)
			objc.Send[struct{}](window.ID, objc.Sel("release"))
		}
	})

	errCh := make(chan error, 1)
	go refreshQEMUDisplay(ctx, input, refresh, func(img image.Image) {
		runOnUIThreadSync(func() {
			view.SetImage(nsImageFromImage(img))
		})
	}, errCh)

	runAppEventLoopUntil(app, func() bool {
		if closed.Load() {
			return true
		}
		select {
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "qemu-display: refresh: %v\n", err)
			}
			return true
		default:
			return false
		}
	})
	return nil
}

func activateQEMUDisplayApp(app appkit.NSApplication) {
	app.Activate()
	running := appkit.NewRunningApplicationWithProcessIdentifier(int32(os.Getpid()))
	running.ActivateWithOptions(appkit.NSApplicationActivateIgnoringOtherApps)
}

func refreshQEMUDisplay(ctx context.Context, input *qemuDisplayInput, refresh time.Duration, update func(image.Image), errCh chan<- error) {
	t := time.NewTicker(refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			input.ioMu.Lock()
			img, err := input.client.ReadUpdate(readCtx)
			input.ioMu.Unlock()
			cancel()
			if err != nil {
				errCh <- err
				return
			}
			update(img)
		}
	}
}

func qemuDisplayDebug() bool {
	return os.Getenv("COVE_QEMU_DISPLAY_DEBUG") != ""
}

// qemuDisplayLegacyMonitors reports whether the viewer should install global
// NSEvent monitors for input instead of relying on ordinary AppKit first
// responder delivery. The default viewer routes keyboard and pointer events
// through the view's responder methods, which only fire while the Cove window
// is key and the image view is first responder — native focus semantics. The
// legacy global-monitor path predates the in-view tracking area, requires
// Accessibility permission, and forwards events even when the window is not
// focused; it remains available as an opt-in recovery path.
func qemuDisplayLegacyMonitors() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVE_QEMU_LEGACY_MONITORS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// qemuDisplayInputMode names the input-delivery path the viewer uses, for
// status reporting and support bundles.
func qemuDisplayInputMode() string {
	if qemuDisplayLegacyMonitors() {
		return "global-monitor"
	}
	return "responder"
}

func imageContentRect(img image.Image) corefoundation.CGRect {
	b := img.Bounds()
	width, height := float64(b.Dx()), float64(b.Dy())
	if width <= 0 {
		width = 800
	}
	if height <= 0 {
		height = 600
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 100, Y: 100},
		Size:   corefoundation.CGSize{Width: width, Height: height},
	}
}

func nsImageFromImage(img image.Image) appkit.NSImage {
	data, err := encodeWindowsQEMUImage(img, "jpeg")
	if err != nil {
		return appkit.NSImage{}
	}
	nsData := foundation.NewDataWithBytesLength(data)
	return appkit.NewImageWithData(nsData)
}

type qemuDisplayInput struct {
	client *rfb.Client
	size   image.Point

	ioMu    sync.Mutex
	mu      sync.Mutex
	buttons uint8
	active  bool
}

func newQEMUDisplayImageView(frame corefoundation.CGRect, client *rfb.Client, size image.Point) (appkit.NSImageView, *qemuDisplayInput) {
	input := &qemuDisplayInput{client: client, size: size}
	className := fmt.Sprintf("CoveQEMUDisplayImageView_%d", qemuDisplayViewClassCounter.Add(1))
	methods := []objc.MethodDef{
		{
			Cmd: objc.RegisterName("acceptsFirstResponder"),
			Fn: func(self objc.ID, _cmd objc.SEL) bool {
				return true
			},
		},
		{
			Cmd: objc.RegisterName("acceptsFirstMouse:"),
			Fn: func(self objc.ID, _cmd objc.SEL, event objc.ID) bool {
				return true
			},
		},
		{
			Cmd: objc.RegisterName("mouseMoved:"),
			Fn: func(self objc.ID, _cmd objc.SEL, event objc.ID) {
				input.sendMouse(self, appkit.NSEventFromID(event), 0, false)
			},
		},
		{
			Cmd: objc.RegisterName("mouseDown:"),
			Fn: func(self objc.ID, _cmd objc.SEL, event objc.ID) {
				input.sendMouse(self, appkit.NSEventFromID(event), 1, true)
			},
		},
		{
			Cmd: objc.RegisterName("mouseDragged:"),
			Fn: func(self objc.ID, _cmd objc.SEL, event objc.ID) {
				input.sendMouse(self, appkit.NSEventFromID(event), 0, false)
			},
		},
		{
			Cmd: objc.RegisterName("mouseUp:"),
			Fn: func(self objc.ID, _cmd objc.SEL, event objc.ID) {
				input.sendMouse(self, appkit.NSEventFromID(event), 1, false)
			},
		},
		{
			Cmd: objc.RegisterName("rightMouseDown:"),
			Fn: func(self objc.ID, _cmd objc.SEL, event objc.ID) {
				input.sendMouse(self, appkit.NSEventFromID(event), 4, true)
			},
		},
		{
			Cmd: objc.RegisterName("rightMouseDragged:"),
			Fn: func(self objc.ID, _cmd objc.SEL, event objc.ID) {
				input.sendMouse(self, appkit.NSEventFromID(event), 0, false)
			},
		},
		{
			Cmd: objc.RegisterName("rightMouseUp:"),
			Fn: func(self objc.ID, _cmd objc.SEL, event objc.ID) {
				input.sendMouse(self, appkit.NSEventFromID(event), 4, false)
			},
		},
		{
			Cmd: objc.RegisterName("keyDown:"),
			Fn: func(self objc.ID, _cmd objc.SEL, event objc.ID) {
				input.sendKey(appkit.NSEventFromID(event))
			},
		},
		{
			// updateTrackingAreas keeps a full-bounds tracking area so the
			// view receives mouseMoved: through the ordinary responder chain
			// while it is in the key window. Without it AppKit never delivers
			// mouseMoved:, which is why earlier builds fell back to a global
			// event monitor.
			Cmd: objc.RegisterName("updateTrackingAreas"),
			Fn: func(self objc.ID, _cmd objc.SEL) {
				view := appkit.NSViewFromID(self)
				for _, area := range view.TrackingAreas() {
					view.RemoveTrackingArea(area)
				}
				area := appkit.NewTrackingAreaWithRectOptionsOwnerUserInfo(
					view.Bounds(),
					appkit.NSTrackingMouseMoved|
						appkit.NSTrackingActiveInKeyWindow|
						appkit.NSTrackingInVisibleRect,
					objectivec.ObjectFromID(self),
					nil,
				)
				view.AddTrackingArea(area)
			},
		},
	}
	cls, err := objc.RegisterClass(className, objc.GetClass("NSImageView"), nil, nil, methods)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qemu-display: register input view: %v\n", err)
		return appkit.NewImageViewWithFrame(frame), input
	}
	alloc := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	viewID := objc.Send[objc.ID](alloc, objc.Sel("initWithFrame:"), frame)
	view := appkit.NSImageViewFromID(viewID)
	view.NSView.UpdateTrackingAreas()
	return view, input
}

func (i *qemuDisplayInput) sendMouse(self objc.ID, event appkit.NSEvent, button uint8, down bool) {
	x, y := i.eventPoint(self, event)
	if qemuDisplayDebug() {
		fmt.Fprintf(os.Stderr, "qemu-display: mouse x=%d y=%d button=%d down=%v\n", x, y, button, down)
	}
	i.mu.Lock()
	if button != 0 {
		if down {
			i.buttons |= button
			i.active = true
		} else {
			i.buttons &^= button
		}
	}
	buttons := i.buttons
	i.mu.Unlock()
	go func() {
		i.ioMu.Lock()
		defer i.ioMu.Unlock()
		if err := i.client.Pointer(x, y, buttons); err != nil {
			fmt.Fprintf(os.Stderr, "qemu-display: mouse: %v\n", err)
		}
	}()
}

func (i *qemuDisplayInput) eventPoint(self objc.ID, event appkit.NSEvent) (int, int) {
	view := appkit.NSViewFromID(self)
	p := view.ConvertPointFromView(event.LocationInWindow(), appkit.NSView{})
	bounds := view.Bounds().Size
	width, height := bounds.Width, bounds.Height
	if width <= 0 {
		width = float64(i.size.X)
	}
	if height <= 0 {
		height = float64(i.size.Y)
	}
	x := int((p.X / width * float64(i.size.X)) + 0.5)
	y := int(((height - p.Y) / height * float64(i.size.Y)) + 0.5)
	x = max(0, min(x, i.size.X-1))
	y = max(0, min(y, i.size.Y-1))
	return x, y
}

func (i *qemuDisplayInput) sendMousePoint(x, y int, button uint8, down bool) {
	if qemuDisplayDebug() {
		fmt.Fprintf(os.Stderr, "qemu-display: global mouse x=%d y=%d button=%d down=%v\n", x, y, button, down)
	}
	i.mu.Lock()
	if button != 0 {
		if down {
			i.buttons |= button
			i.active = true
		} else {
			i.buttons &^= button
		}
	}
	buttons := i.buttons
	i.mu.Unlock()
	go func() {
		i.ioMu.Lock()
		defer i.ioMu.Unlock()
		if err := i.client.Pointer(x, y, buttons); err != nil {
			fmt.Fprintf(os.Stderr, "qemu-display: mouse: %v\n", err)
		}
	}()
}

func (i *qemuDisplayInput) sendKey(event appkit.NSEvent) {
	if event.ModifierFlags()&(appkit.NSEventModifierFlagCommand|appkit.NSEventModifierFlagControl|appkit.NSEventModifierFlagOption) != 0 {
		return
	}
	text := event.Characters()
	if text == "" {
		return
	}
	if qemuDisplayDebug() {
		fmt.Fprintf(os.Stderr, "qemu-display: key %q\n", text)
	}
	go func() {
		i.ioMu.Lock()
		defer i.ioMu.Unlock()
		if err := i.client.TypeText(text); err != nil {
			fmt.Fprintf(os.Stderr, "qemu-display: key: %v\n", err)
		}
	}()
}

func (i *qemuDisplayInput) activeForKeys() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.active
}

func (i *qemuDisplayInput) deactivate() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.active = false
	i.buttons = 0
}

func installQEMUDisplayGlobalMonitors(window appkit.NSWindow, view appkit.NSView, input *qemuDisplayInput) []objectivec.IObject {
	if input == nil {
		return nil
	}
	events := appkit.GetNSEventClass()
	mouseMask := appkit.NSEventMaskMouseMoved |
		appkit.NSEventMaskLeftMouseDown |
		appkit.NSEventMaskLeftMouseDragged |
		appkit.NSEventMaskLeftMouseUp |
		appkit.NSEventMaskRightMouseDown |
		appkit.NSEventMaskRightMouseDragged |
		appkit.NSEventMaskRightMouseUp
	keyMask := appkit.NSEventMaskKeyDown
	return []objectivec.IObject{
		events.AddGlobalMonitorForEventsMatchingMaskHandler(mouseMask, func(event *appkit.NSEvent) {
			if event == nil {
				return
			}
			x, y, ok := qemuDisplayPointFromScreen(window, view, input.size, event.LocationInWindow())
			if !ok {
				if event.Type() == appkit.NSEventTypeLeftMouseDown || event.Type() == appkit.NSEventTypeRightMouseDown {
					input.deactivate()
				}
				return
			}
			switch event.Type() {
			case appkit.NSEventTypeLeftMouseDown:
				input.sendMousePoint(x, y, 1, true)
			case appkit.NSEventTypeLeftMouseUp:
				input.sendMousePoint(x, y, 1, false)
			case appkit.NSEventTypeRightMouseDown:
				input.sendMousePoint(x, y, 4, true)
			case appkit.NSEventTypeRightMouseUp:
				input.sendMousePoint(x, y, 4, false)
			default:
				input.sendMousePoint(x, y, 0, false)
			}
		}),
		events.AddGlobalMonitorForEventsMatchingMaskHandler(keyMask, func(event *appkit.NSEvent) {
			if event == nil || !input.activeForKeys() {
				return
			}
			input.sendKey(*event)
		}),
	}
}

func qemuDisplayPointFromScreen(window appkit.NSWindow, view appkit.NSView, size image.Point, screen corefoundation.CGPoint) (int, int, bool) {
	if window.ID == 0 || view.ID == 0 || size.X <= 0 || size.Y <= 0 {
		return 0, 0, false
	}
	p := view.ConvertPointFromView(window.ConvertPointFromScreen(screen), appkit.NSView{})
	bounds := view.Bounds().Size
	width, height := bounds.Width, bounds.Height
	if width <= 0 || height <= 0 || p.X < 0 || p.Y < 0 || p.X > width || p.Y > height {
		return 0, 0, false
	}
	x := int((p.X / width * float64(size.X)) + 0.5)
	y := int(((height - p.Y) / height * float64(size.Y)) + 0.5)
	return max(0, min(x, size.X-1)), max(0, min(y, size.Y-1)), true
}

func newQEMUDisplayMenuTarget(vmDir string, closed *atomic.Bool, window *appkit.NSWindow) objc.ID {
	className := fmt.Sprintf("CoveQEMUDisplayMenuTarget_%d", qemuDisplayMenuClassCounter.Add(1))
	cls, err := objc.RegisterClass(
		className,
		objc.GetClass("NSObject"),
		nil, nil,
		[]objc.MethodDef{
			{
				Cmd: objc.RegisterName("closeQEMUDisplay:"),
				Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID) {
					closed.Store(true)
					if window != nil && window.ID != 0 {
						window.Close()
					}
				},
			},
			{
				Cmd: objc.RegisterName("takeQEMUDisplayScreenshot:"),
				Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID) {
					go func() {
						img, err := captureWindowsQEMUImage(vmDir)
						if err != nil {
							fmt.Fprintf(os.Stderr, "qemu-display: screenshot: %v\n", err)
							return
						}
						path, err := writeWindowsQEMUGUIDiagnoseScreenshot(vmDir, img)
						if err != nil {
							fmt.Fprintf(os.Stderr, "qemu-display: screenshot: %v\n", err)
							return
						}
						fmt.Fprintf(os.Stderr, "qemu-display: screenshot saved to %s\n", path)
					}()
				},
			},
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qemu-display: register menu target: %v\n", err)
		return 0
	}
	target := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	return objc.Send[objc.ID](target, objc.Sel("init"))
}

func setupQEMUDisplayMainMenu(target objc.ID) {
	app := getSharedApp()
	mainMenu := appkit.NewMenuWithTitle("")

	appMenu := appkit.NewMenuWithTitle("")
	addMainMenuItem(appMenu, "About cove", "orderFrontStandardAboutPanel:", "", 0)
	addMainMenuSeparator(appMenu)
	addMainMenuItem(appMenu, "Close QEMU Viewer", "closeQEMUDisplay:", "w", target)
	addMainMenuItem(appMenu, "Quit cove", "closeQEMUDisplay:", "q", target)
	appMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("", 0, "")
	appMenuItem.SetSubmenu(&appMenu)
	mainMenu.AddItem(&appMenuItem)

	editMenu := appkit.NewMenuWithTitle("Edit")
	addMainMenuItem(editMenu, "Copy", "copy:", "c", 0)
	addMainMenuItem(editMenu, "Paste", "paste:", "v", 0)
	editMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Edit", 0, "")
	editMenuItem.SetSubmenu(&editMenu)
	mainMenu.AddItem(&editMenuItem)

	vmMenu := appkit.NewMenuWithTitle("VM")
	addMainMenuItem(vmMenu, "Screenshot...", "takeQEMUDisplayScreenshot:", "s", target)
	addMainMenuSeparator(vmMenu)
	addMainMenuItem(vmMenu, "Close Viewer", "closeQEMUDisplay:", "w", target)
	vmMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("VM", 0, "")
	vmMenuItem.SetSubmenu(&vmMenu)
	mainMenu.AddItem(&vmMenuItem)

	viewMenu := appkit.NewMenuWithTitle("View")
	addMainMenuItem(viewMenu, "Toggle Toolbar", "toggleToolbarShown:", "t", 0)
	addMainMenuItemWithModifiers(viewMenu, "Enter Full Screen", "toggleFullScreen:", "f", 0,
		appkit.NSEventModifierFlagCommand|appkit.NSEventModifierFlagControl)
	viewMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("View", 0, "")
	viewMenuItem.SetSubmenu(&viewMenu)
	mainMenu.AddItem(&viewMenuItem)

	windowMenu := appkit.NewMenuWithTitle("Window")
	addMainMenuItem(windowMenu, "Minimize", "performMiniaturize:", "m", 0)
	addMainMenuItem(windowMenu, "Zoom", "performZoom:", "", 0)
	addMainMenuSeparator(windowMenu)
	addMainMenuItem(windowMenu, "Bring All to Front", "arrangeInFront:", "", 0)
	windowMenuItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Window", 0, "")
	windowMenuItem.SetSubmenu(&windowMenu)
	mainMenu.AddItem(&windowMenuItem)

	objc.Send[objc.ID](app.ID, objc.Sel("setWindowsMenu:"), windowMenu.ID)
	objc.Send[objc.ID](app.ID, objc.Sel("setMainMenu:"), mainMenu.ID)
}

func setupQEMUDisplayToolbar(window appkit.NSWindow, target objc.ID) objc.ID {
	if window.ID == 0 || target == 0 {
		return 0
	}
	itemIDs := []string{"qemuScreenshot", string(appkit.ToolbarFlexibleSpaceItemIdentifier), "qemuClose"}
	className := fmt.Sprintf("CoveQEMUDisplayToolbarDelegate_%d", qemuDisplayToolbarClassCounter.Add(1))
	cls, err := objc.RegisterClass(
		className,
		objc.GetClass("NSObject"),
		nil, nil,
		[]objc.MethodDef{
			{
				Cmd: objc.RegisterName("toolbar:itemForItemIdentifier:willBeInsertedIntoToolbar:"),
				Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, identifierID objc.ID, _ bool) objc.ID {
					identifier := foundation.NSStringFromID(identifierID).String()
					var item appkit.NSToolbarItem
					switch identifier {
					case "qemuScreenshot":
						item = qemuDisplayToolbarItem(identifier, "camera", "Screenshot", "takeQEMUDisplayScreenshot:", target)
					case "qemuClose":
						item = qemuDisplayToolbarItem(identifier, "xmark.circle", "Close Viewer", "closeQEMUDisplay:", target)
					default:
						return 0
					}
					objc.Send[objc.ID](item.ID, objc.Sel("retain"))
					return item.ID
				},
			},
			{
				Cmd: objc.RegisterName("toolbarDefaultItemIdentifiers:"),
				Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID) objc.ID {
					return objectivec.StringSliceToNSArray(itemIDs)
				},
			},
			{
				Cmd: objc.RegisterName("toolbarAllowedItemIdentifiers:"),
				Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID) objc.ID {
					return objectivec.StringSliceToNSArray(itemIDs)
				},
			},
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qemu-display: register toolbar delegate: %v\n", err)
		return 0
	}
	delegate := objc.Send[objc.ID](objc.ID(cls), objc.Sel("alloc"))
	delegate = objc.Send[objc.ID](delegate, objc.Sel("init"))
	toolbar := appkit.NewToolbarWithIdentifier("com.cove.qemuDisplayToolbar")
	toolbar.SetDisplayMode(nsToolbarDisplayModeIconOnly)
	toolbar.SetDelegate(appkit.NSToolbarDelegateObjectFromID(delegate))
	window.SetToolbar(&toolbar)
	window.SetToolbarStyle(appkit.NSWindowToolbarStyleUnified)
	return delegate
}

func qemuDisplayToolbarItem(id, sfSymbol, label, action string, target objc.ID) appkit.NSToolbarItem {
	item := appkit.NewToolbarItemWithItemIdentifier(appkit.NSToolbarItemIdentifier(id))
	img := appkit.NewImageWithSystemSymbolNameAccessibilityDescription(sfSymbol, label)
	item.SetImage(&img)
	item.SetLabel(label)
	item.SetPaletteLabel(label)
	item.SetToolTip(label)
	item.SetBordered(true)
	item.SetAutovalidates(false)
	item.SetTarget(objectivec.ObjectFromID(target))
	item.SetAction(objectivec.SEL(objc.Sel(action)))
	return item
}
