// boot_commands.go - Recovery automation helpers shared by unattended setup and vzscript
package main

import (
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/apple/appkit"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// automationExecutor runs Recovery automation steps against a VM.
type automationExecutor struct {
	ocr      *OCRService
	cs       *ControlServer
	verbose  bool
	debugDir string // if set, save debug screenshots here
}

// newAutomationExecutor creates a new executor.
func newAutomationExecutor(ocr *OCRService, cs *ControlServer, verbose bool, debugDir string) *automationExecutor {
	return &automationExecutor{
		ocr:      ocr,
		cs:       cs,
		verbose:  verbose,
		debugDir: debugDir,
	}
}

func (e *automationExecutor) waitForText(text string, timeout time.Duration) error {
	return e.waitForTextWithOptions(text, timeout, OCRSearchOptions{})
}

func (e *automationExecutor) waitForTextWithOptions(text string, timeout time.Duration, opts OCRSearchOptions) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img := e.captureScreen()
		if img == nil {
			time.Sleep(time.Second)
			continue
		}
		_, _, found := e.ocr.FindTextWithOptions(img, text, opts)
		if found {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout waiting for text %q", text)
}

func (e *automationExecutor) clickText(text string, timeout time.Duration) error {
	return e.clickTextWithOptions(text, timeout, OCRSearchOptions{})
}

func (e *automationExecutor) clickTextWithOptions(text string, timeout time.Duration, opts OCRSearchOptions) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img := e.captureScreen()
		if img == nil {
			time.Sleep(time.Second)
			continue
		}
		x, y, found := e.ocr.FindTextWithOptions(img, text, opts)
		if found {
			return e.clickAt(x, y, img.Bounds().Dx(), img.Bounds().Dy())
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout waiting for text %q to click", text)
}

func (e *automationExecutor) hostClickTextWithOptions(text string, timeout time.Duration, opts OCRSearchOptions) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img := e.captureScreen()
		if img == nil {
			time.Sleep(time.Second)
			continue
		}
		x, y, found := e.ocr.FindTextWithOptions(img, text, opts)
		if found {
			return e.hostClickPixel(x, y)
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout waiting for text %q to host-click", text)
}

func (e *automationExecutor) hostClickTextIfPresent(text string, timeout time.Duration, opts OCRSearchOptions) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img := e.captureScreen()
		if img == nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		x, y, found := e.ocr.FindTextWithOptions(img, text, opts)
		if found {
			return e.hostClickPixel(x, y)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func (e *automationExecutor) activateStartupOptions(timeout time.Duration) error {
	if e.cs == nil {
		return fmt.Errorf("startupOptions requires control server")
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img := e.captureScreen()
		if img == nil {
			time.Sleep(time.Second)
			continue
		}

		width := float64(img.Bounds().Dx())
		height := float64(img.Bounds().Dy())
		if width == 0 || height == 0 {
			time.Sleep(250 * time.Millisecond)
			continue
		}

		if continueX, continueY, continueFound := e.ocr.FindTextWithOptions(img, "Continue", OCRSearchOptions{}); continueFound && continueBelongsToOptions(width, continueX) {
			if err := e.sendKey("return"); err == nil {
				time.Sleep(500 * time.Millisecond)
				return nil
			}
			if err := e.clickAt(continueX, continueY, int(width), int(height)); err == nil {
				time.Sleep(350 * time.Millisecond)
				return nil
			}
		}

		for _, key := range []string{"right", "right", "return"} {
			if err := e.sendKey(key); err != nil {
				return err
			}
			time.Sleep(350 * time.Millisecond)

			img = e.captureScreen()
			if img == nil {
				continue
			}
			width = float64(img.Bounds().Dx())
			height = float64(img.Bounds().Dy())
			continueX, continueY, continueFound := e.ocr.FindTextWithOptions(img, "Continue", OCRSearchOptions{})
			if continueFound && continueBelongsToOptions(width, continueX) {
				if err := e.sendKey("return"); err == nil {
					time.Sleep(500 * time.Millisecond)
					return nil
				}
				if err := e.clickAt(continueX, continueY, img.Bounds().Dx(), img.Bounds().Dy()); err == nil {
					time.Sleep(350 * time.Millisecond)
					return nil
				}
				return nil
			}
			if len(ocrTexts(e.ocr, img)) == 0 {
				return nil
			}
		}

		optX, optY, found := e.ocr.FindTextWithOptions(img, "Options", OCRSearchOptions{})
		if !found {
			time.Sleep(time.Second)
			continue
		}

		clicked := false
		for _, pt := range startupOptionsTilePoints(width, height, optX, optY) {
			x := pt.X
			y := pt.Y
			if y < 0 {
				y = 0
			}
			if err := e.clickAt(x, y, int(width), int(height)); err != nil {
				return err
			}
			clicked = true
			time.Sleep(350 * time.Millisecond)

			img = e.captureScreen()
			if img != nil {
				continueX, continueY, continueFound := e.ocr.FindTextWithOptions(img, "Continue", OCRSearchOptions{})
				if continueFound && continueBelongsToOptions(width, continueX) {
					if err := e.sendKey("return"); err == nil {
						time.Sleep(500 * time.Millisecond)
						return nil
					}
					time.Sleep(350 * time.Millisecond)
					return e.clickAt(continueX, continueY, int(width), int(height))
				}
			}

			// Startup Options behaves like a tile selector. After selecting the
			// tile, Return is often the reliable activation gesture even when the
			// Continue button is not yet OCR-visible.
			if err := e.sendKey("return"); err != nil {
				return err
			}
			time.Sleep(500 * time.Millisecond)

			img = e.captureScreen()
			if img != nil {
				continueX, continueY, continueFound := e.ocr.FindTextWithOptions(img, "Continue", OCRSearchOptions{})
				if continueFound && continueBelongsToOptions(width, continueX) {
					if err := e.sendKey("return"); err == nil {
						time.Sleep(500 * time.Millisecond)
						return nil
					}
					time.Sleep(350 * time.Millisecond)
					return e.clickAt(continueX, continueY, int(width), int(height))
				}
				if len(ocrTexts(e.ocr, img)) == 0 {
					return nil
				}
			}
		}
		if clicked {
			blankSince := time.Now()
			for time.Since(blankSince) < 4*time.Second {
				img = e.captureScreen()
				if img == nil {
					time.Sleep(250 * time.Millisecond)
					continue
				}
				if len(ocrTexts(e.ocr, img)) == 0 {
					return nil
				}
				time.Sleep(250 * time.Millisecond)
			}
			time.Sleep(800 * time.Millisecond)
		}
	}

	return fmt.Errorf("timeout activating Recovery Startup Options")
}

type startupClickPoint struct {
	X float64
	Y float64
}

func startupOptionsTilePoints(width, height, optX, optY float64) []startupClickPoint {
	points := []startupClickPoint{
		{X: 0.61 * width, Y: 0.45 * height},
		{X: 0.61 * width, Y: 0.48 * height},
		{X: 0.58 * width, Y: 0.45 * height},
		{X: 0.64 * width, Y: 0.45 * height},
	}
	for _, xOffsetNorm := range []float64{0.00, -0.02, 0.02} {
		for _, yOffsetNorm := range []float64{0.11, 0.09, 0.07} {
			points = append(points, startupClickPoint{
				X: optX + xOffsetNorm*width,
				Y: optY - yOffsetNorm*height,
			})
		}
	}
	return points
}

func continueBelongsToOptions(width, continueX float64) bool {
	return continueX >= width*0.5
}

func ocrTexts(ocr *OCRService, img image.Image) []string {
	if ocr == nil || img == nil {
		return nil
	}
	obs, err := ocr.RecognizeText(img)
	if err != nil {
		return nil
	}
	texts := make([]string, 0, len(obs))
	for _, ob := range obs {
		if strings.TrimSpace(ob.Text) != "" {
			texts = append(texts, ob.Text)
		}
	}
	return texts
}

func (e *automationExecutor) continueRecoveryLanguage(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	advanced := false
	for time.Now().Before(deadline) {
		if e.hasHostMenuTitle("Utilities") {
			return nil
		}

		img := e.captureScreen()
		if img == nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if _, _, found := e.ocr.FindTextWithOptions(img, "Utilities", OCRMenuSearchOptions()); found {
			return nil
		}

		if continueX, continueY, found := e.ocr.FindTextWithOptions(img, "Continue", OCRSearchOptions{}); found {
			if err := e.sendKey("return"); err != nil {
				return err
			}
			time.Sleep(500 * time.Millisecond)
			next := e.captureScreen()
			if next != nil {
				if _, _, stillVisible := e.ocr.FindTextWithOptions(next, "Continue", OCRSearchOptions{}); stillVisible {
					if err := e.clickAt(continueX, continueY, img.Bounds().Dx(), img.Bounds().Dy()); err != nil {
						return err
					}
				}
			}
			advanced = true
			time.Sleep(2 * time.Second)
			continue
		}

		if _, _, found := e.ocr.FindTextWithOptions(img, "Language", OCRSearchOptions{}); !found {
			if advanced {
				return nil
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// On the Recovery language sheet, Return advances the selected language
		// reliably. Pointer input on the arrow button is inconsistent here.
		if err := e.sendKey("return"); err != nil {
			return err
		}
		advanced = true
		time.Sleep(2 * time.Second)
	}
	if advanced {
		return fmt.Errorf("timeout leaving Recovery language page")
	}
	return nil
}

func (e *automationExecutor) clickMenuItem(menu, item string, timeout time.Duration) error {
	if e.clickHostMenuItem(menu, item) {
		return nil
	}

	opts := OCRMenuSearchOptions()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := e.waitForTextWithOptions(menu, 3*time.Second, opts); err != nil {
			continue
		}
		if err := e.hostClickTextWithOptions(menu, 2*time.Second, opts); err != nil {
			continue
		}
		time.Sleep(250 * time.Millisecond)

		itemDeadline := time.Now().Add(2 * time.Second)
		if itemDeadline.After(deadline) {
			itemDeadline = deadline
		}
		for time.Now().Before(itemDeadline) {
			img := e.captureScreen()
			if img == nil {
				time.Sleep(150 * time.Millisecond)
				continue
			}
			x, y, found := e.ocr.FindTextWithOptions(img, item, opts)
			if found {
				return e.hostClickPixel(x, y)
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	return fmt.Errorf("timeout clicking menu item %q from menu %q", item, menu)
}

func (e *automationExecutor) hasHostMenuTitle(title string) bool {
	if e.cs == nil {
		return false
	}

	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}

	var found bool
	DispatchSync(GetMainDispatchQueue(), func() {
		mainMenu := appkit.NSMenuFromID(getSharedApp().MainMenu().GetID())
		if mainMenu.ID == 0 {
			if e.verbose {
				fmt.Printf("[boot] host menu lookup %q: no main menu\n", title)
			}
			return
		}
		item := appkit.NSMenuItemFromID(mainMenu.ItemWithTitle(title).GetID())
		found = item.ID != 0
	})
	if e.verbose {
		fmt.Printf("[boot] host menu lookup %q: found=%v\n", title, found)
	}
	return found
}

func (e *automationExecutor) clickHostMenuItem(menuTitle, itemTitle string) bool {
	if e.cs == nil {
		return false
	}

	menuTitle = strings.TrimSpace(menuTitle)
	itemTitle = strings.TrimSpace(itemTitle)
	if menuTitle == "" || itemTitle == "" {
		return false
	}

	var clicked bool
	var reason string
	DispatchSync(GetMainDispatchQueue(), func() {
		app := getSharedApp()
		mainMenu := appkit.NSMenuFromID(app.MainMenu().GetID())
		if mainMenu.ID == 0 {
			reason = "no main menu"
			return
		}

		menuItem := appkit.NSMenuItemFromID(mainMenu.ItemWithTitle(menuTitle).GetID())
		if menuItem.ID == 0 || !menuItem.HasSubmenu() {
			reason = "menu missing or has no submenu"
			return
		}

		submenu := appkit.NSMenuFromID(menuItem.Submenu().GetID())
		if submenu.ID == 0 {
			reason = "submenu missing"
			return
		}

		subItem := appkit.NSMenuItemFromID(submenu.ItemWithTitle(itemTitle).GetID())
		if subItem.ID == 0 {
			reason = "submenu item missing"
			return
		}

		idx := submenu.IndexOfItem(subItem)
		if idx < 0 {
			reason = "submenu item index missing"
			return
		}

		submenu.PerformActionForItemAtIndex(idx)
		clicked = true
		reason = "performed action"
	})
	if e.verbose {
		fmt.Printf("[boot] host menu click %q -> %q: clicked=%v reason=%s\n", menuTitle, itemTitle, clicked, reason)
	}
	return clicked
}

func (e *automationExecutor) typeText(text string) error {
	resp := e.cs.typeText(&controlpb.TextCommand{Text: text})
	if !resp.Success {
		return fmt.Errorf("type text: %s", resp.Error)
	}
	return nil
}

func (e *automationExecutor) typeTextKeycodes(text string) error {
	for _, ch := range text {
		info, ok := charToKeyCode[ch]
		if !ok {
			return fmt.Errorf("no keycode for %q", ch)
		}

		var modifiers uint32
		if info.shift {
			modifiers = uint32(ModifierShift)
		}
		useCGEvent := e.cs != nil && e.cs.inputBackend() == automationBackendWindow

		resp := e.cs.sendKeyEvent(&controlpb.KeyCommand{
			KeyCode:    uint32(info.keyCode),
			KeyDown:    true,
			Modifiers:  modifiers,
			Character:  string(ch),
			UseCgEvent: useCGEvent,
		})
		if !resp.Success {
			return fmt.Errorf("type key down %q: %s", ch, resp.Error)
		}
		time.Sleep(30 * time.Millisecond)

		resp = e.cs.sendKeyEvent(&controlpb.KeyCommand{
			KeyCode:    uint32(info.keyCode),
			KeyDown:    false,
			Modifiers:  modifiers,
			Character:  string(ch),
			UseCgEvent: useCGEvent,
		})
		if !resp.Success {
			return fmt.Errorf("type key up %q: %s", ch, resp.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func (e *automationExecutor) typeAndReturnIfText(needle, value string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img := e.captureScreen()
		if img == nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		lowerNeedle := strings.ToLower(needle)
		if strings.Contains(lowerNeedle, "password") && e.recoveryAuthFailed(img) {
			return fmt.Errorf("%s", recoveryAuthFailureMessage(needle))
		}
		_, _, found := e.ocr.FindText(img, needle)
		if found {
			typeFn := e.typeText
			if strings.Contains(lowerNeedle, "password") ||
				strings.Contains(lowerNeedle, "[y/n]") ||
				strings.Contains(lowerNeedle, "are you sure") ||
				strings.Contains(lowerNeedle, "allow booting unsigned") {
				// Recovery's secure-password prompt can render before Terminal is
				// and confirmation prompts can render before Terminal is actually
				// ready to consume injected text. A short settle plus raw keycode
				// entry behaves more like physical typing than Unicode text input.
				time.Sleep(1500 * time.Millisecond)
				typeFn = e.typeTextKeycodes
			}
			if err := typeFn(value); err != nil {
				return err
			}
			if err := e.sendKey("return"); err != nil {
				return err
			}

			clearSatisfied := func(img image.Image) bool {
				return promptClearedOCR(e.ocr, img, needle)
			}

			// Wait for the prompt to actually clear before allowing the next
			// step to run. Recovery screens can lag behind the injected key
			// event, and without this a later prompt step can re-match the same
			// visible line and inject the same answer twice.
			clearDeadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(clearDeadline) {
				img = e.captureScreen()
				if img == nil {
					time.Sleep(250 * time.Millisecond)
					continue
				}
				if strings.Contains(lowerNeedle, "password") && e.recoveryAuthFailed(img) {
					return fmt.Errorf("%s", recoveryAuthFailureMessage(needle))
				}
				if clearSatisfied(img) {
					return nil
				}
				time.Sleep(250 * time.Millisecond)
			}
			return fmt.Errorf("timeout waiting for prompt %q to clear", needle)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func promptClearedOCR(ocr *OCRService, img image.Image, needle string) bool {
	if img == nil {
		return false
	}
	if _, _, found := ocr.FindText(img, needle); !found {
		return true
	}
	return pageContainsAnyOCR(ocr, img, promptClearTexts(needle)...)
}

func pageContainsAnyOCR(ocr *OCRService, img image.Image, texts ...string) bool {
	for _, text := range texts {
		if _, _, found := ocr.FindText(img, text); found {
			return true
		}
	}
	return false
}

func promptClearTexts(needle string) []string {
	lowerNeedle := strings.ToLower(needle)
	if strings.Contains(lowerNeedle, "[y/n]") ||
		strings.Contains(lowerNeedle, "allow booting unsigned") ||
		strings.Contains(lowerNeedle, "are you sure") {
		return []string{
			"password for user",
			"Password",
			"Authorized user",
			"Enter password",
			"Authentication failure",
			"System Integrity Protection is",
			"System Integrity Protection is off",
			"System Integrity Protection is enabled",
			"Restart the machine",
			"-bash-3.2#",
		}
	}
	if strings.Contains(lowerNeedle, "authorized user") ||
		strings.Contains(lowerNeedle, "enter username") ||
		strings.Contains(lowerNeedle, "user name") {
		return []string{
			"Password",
			"Enter password",
			"password for user",
			"Unknown user",
			"Authentication failure",
			"System Integrity Protection is",
			"System Integrity Protection is off",
			"System Integrity Protection is enabled",
			"Restart the machine",
			"-bash-3.2#",
		}
	}
	if strings.Contains(lowerNeedle, "password") {
		return []string{
			"Authentication failure",
			"System Integrity Protection is",
			"System Integrity Protection is off",
			"System Integrity Protection is enabled",
			"Restart the machine",
			"-bash-3.2#",
		}
	}
	return nil
}

func (e *automationExecutor) pageContainsAny(img image.Image, texts ...string) bool {
	return pageContainsAnyOCR(e.ocr, img, texts...)
}

func (e *automationExecutor) recoveryAuthFailed(img image.Image) bool {
	return e.pageContainsAny(img,
		"Authentication failure",
		"Failed to authenticate",
		"failed to set credential",
	)
}

func recoveryAuthFailureMessage(needle string) string {
	return fmt.Sprintf("recovery authentication failed after answering prompt %q; the VM user is not authorized for recovery operations. Ensure provisioning completed with bootstrap recovery enabled and that 'diskutil apfs updatePreboot /' ran successfully", needle)
}

func (e *automationExecutor) rebootIfText(needle string) error {
	img := e.captureScreen()
	if img == nil {
		return fmt.Errorf("capture screen for rebootIfText")
	}
	if _, _, found := e.ocr.FindText(img, needle); !found {
		if e.verbose {
			fmt.Printf("[boot] rebootIfText skipped: %q not visible\n", needle)
		}
		return nil
	}
	if err := e.typeText("reboot"); err != nil {
		return err
	}
	return e.sendKey("return")
}

func (e *automationExecutor) sendKey(keySpec string) error {
	keyCode, modifiers := parseKeySpec(keySpec)
	useCGEvent := e.cs != nil && e.cs.inputBackend() == automationBackendWindow
	resp := e.cs.sendKeyEvent(&controlpb.KeyCommand{
		KeyCode:    uint32(keyCode),
		KeyDown:    true,
		Modifiers:  uint32(modifiers),
		UseCgEvent: useCGEvent,
	})
	if !resp.Success {
		return fmt.Errorf("send key: %s", resp.Error)
	}
	time.Sleep(50 * time.Millisecond)
	resp = e.cs.sendKeyEvent(&controlpb.KeyCommand{
		KeyCode:    uint32(keyCode),
		KeyDown:    false,
		Modifiers:  uint32(modifiers),
		UseCgEvent: useCGEvent,
	})
	if !resp.Success {
		return fmt.Errorf("send key up: %s", resp.Error)
	}
	return nil
}

func (e *automationExecutor) clickAt(x, y float64, screenWidth, screenHeight int) error {
	normX := x / float64(screenWidth)
	normY := y / float64(screenHeight)
	resp := e.cs.sendMouseEvent(&controlpb.MouseCommand{
		X:      normX,
		Y:      normY,
		Button: 0,
		Action: "click",
	})
	if !resp.Success {
		return fmt.Errorf("click: %s", resp.Error)
	}
	return nil
}

func (e *automationExecutor) hostClickPixel(x, y float64) error {
	if e.cs == nil {
		return fmt.Errorf("host click requires control server")
	}
	img := e.captureScreen()
	if img != nil {
		if e.verbose {
			fmt.Printf("[boot] safe guest click pixel=(%.1f, %.1f)\n", x, y)
		}
		return e.clickAt(x, y, img.Bounds().Dx(), img.Bounds().Dy())
	}
	return fmt.Errorf("safe click unavailable: no captured screen")
}

func (e *automationExecutor) captureScreen() image.Image {
	if e.cs == nil {
		return nil
	}
	img, errMsg := e.cs.captureDisplayImage()
	if errMsg != "" {
		return nil
	}
	return img
}

func (e *automationExecutor) saveScreenshot() error {
	if e.debugDir == "" {
		return nil
	}
	img := e.captureScreen()
	if img == nil {
		return fmt.Errorf("no screenshot available")
	}
	if e.ocr != nil {
		observations, err := e.ocr.RecognizeText(img)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: OCR recognition failed: %v\n", err)
		}
		saveOCRDebugScreenshot(img, observations, e.debugDir, "boot-cmd")
	}
	return nil
}

// parseKeySpec converts a key specification like "return", "tab", "cmd+q" to keycode + modifiers.
func parseKeySpec(spec string) (keyCode uint16, modifiers uint) {
	parts := strings.Split(strings.ToLower(spec), "+")
	keyName := parts[len(parts)-1]

	for _, mod := range parts[:len(parts)-1] {
		switch mod {
		case "cmd", "command":
			modifiers |= 1 << 20 // kCGEventFlagMaskCommand
		case "shift":
			modifiers |= 1 << 17 // kCGEventFlagMaskShift
		case "ctrl", "control":
			modifiers |= 1 << 18 // kCGEventFlagMaskControl
		case "alt", "option":
			modifiers |= 1 << 19 // kCGEventFlagMaskAlternate
		}
	}

	keyCode = keyNameToCode(keyName)
	return
}

func splitMenuItemArgs(args string) (menu, item string) {
	parts := strings.SplitN(args, "|", 2)
	if len(parts) != 2 {
		return "", ""
	}
	menu = strings.TrimSpace(parts[0])
	item = strings.TrimSpace(parts[1])
	return menu, item
}

func splitConditionalTypeArgs(args string) (needle, value string) {
	parts := strings.SplitN(args, "|", 2)
	if len(parts) != 2 {
		return "", ""
	}
	needle = strings.TrimSpace(parts[0])
	value = strings.TrimSpace(parts[1])
	return needle, value
}

func isValidKeySpec(spec string) bool {
	parts := strings.Split(strings.ToLower(spec), "+")
	if len(parts) == 0 {
		return false
	}

	for _, mod := range parts[:len(parts)-1] {
		switch mod {
		case "cmd", "command", "shift", "ctrl", "control", "alt", "option":
		default:
			return false
		}
	}

	keyName := parts[len(parts)-1]
	if keyName == "" {
		return false
	}

	if keyName == "a" {
		return true // keycode 0 is valid for "a", so we must special-case it.
	}

	if keyNameToCode(keyName) != 0 {
		return true
	}

	_, err := strconv.ParseUint(keyName, 10, 16)
	return err == nil
}

func keyNameToCode(name string) uint16 {
	switch strings.ToLower(name) {
	case "return", "enter":
		return 36
	case "tab":
		return 48
	case "space":
		return 49
	case "escape", "esc":
		return 53
	case "delete", "backspace":
		return 51
	case "up":
		return 126
	case "down":
		return 125
	case "left":
		return 123
	case "right":
		return 124
	case "a":
		return 0
	case "b":
		return 11
	case "c":
		return 8
	case "d":
		return 2
	case "e":
		return 14
	case "f":
		return 3
	case "g":
		return 5
	case "h":
		return 4
	case "i":
		return 34
	case "j":
		return 38
	case "k":
		return 40
	case "l":
		return 37
	case "m":
		return 46
	case "n":
		return 45
	case "o":
		return 31
	case "p":
		return 35
	case "q":
		return 12
	case "r":
		return 15
	case "s":
		return 1
	case "t":
		return 17
	case "u":
		return 32
	case "v":
		return 9
	case "w":
		return 13
	case "x":
		return 7
	case "y":
		return 16
	case "z":
		return 6
	case "f1":
		return 122
	case "f2":
		return 120
	case "f3":
		return 99
	case "f4":
		return 118
	case "f5":
		return 96
	// Number row
	case "0":
		return 29
	case "1":
		return 18
	case "2":
		return 19
	case "3":
		return 20
	case "4":
		return 21
	case "5":
		return 23
	case "6":
		return 22
	case "7":
		return 26
	case "8":
		return 28
	case "9":
		return 25
	// Punctuation
	case "slash":
		return 44
	case "backslash":
		return 42
	case "period", "dot":
		return 47
	case "comma":
		return 43
	case "semicolon":
		return 41
	case "quote", "apostrophe":
		return 39
	case "minus", "dash", "hyphen":
		return 27
	case "equals", "equal":
		return 24
	case "leftbracket", "lbracket":
		return 33
	case "rightbracket", "rbracket":
		return 30
	case "grave", "backtick", "tilde":
		return 50
	default:
		// Try to parse as a numeric keycode
		if code, err := strconv.ParseUint(name, 10, 16); err == nil {
			return uint16(code)
		}
		return 0
	}
}

// unquote removes surrounding quotes from a string.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
