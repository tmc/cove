// boot_commands.go - Boot command DSL parser and executor for unattended setup
package main

import (
	"fmt"
	"image"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// BootCommand represents a single boot automation command.
type BootCommand struct {
	Type string // "wait", "waitForText", "waitForMenuText", "click", "clickMenu", "clickMenuItem", "type", "typeAndReturnIfText", "key", "screenshot"
	Args string // command-specific arguments
}

// ParseBootCommands parses a boot command script into commands.
// Each line is one command. Blank lines and # comments are ignored.
//
// Supported syntax:
//
//	<wait 5s>                  - sleep for duration
//	<waitForText "Continue">   - OCR poll until text appears (timeout 60s)
//	<waitForMenuText "Utilities"> - OCR poll menu bar text (timeout 60s)
//	<click "Continue">         - OCR find text, click its center
//	<clickMenu "Utilities">    - OCR find text in menu bar, click center
//	<clickMenuItem "Utilities|Terminal"> - click menu title then menu item
//	<type "testuser">          - type text string
//	<typeAndReturnIfText "Enter password|secret"> - conditional type+return
//	<key return>               - send key event
//	<key tab>                  - send key event
//	<key cmd+q>                - send key combo
//	<screenshot>               - save debug screenshot
func ParseBootCommands(script string) ([]BootCommand, error) {
	var commands []BootCommand
	lines := strings.Split(script, "\n")

	cmdPattern := regexp.MustCompile(`^<(\w+)\s*(.*)>$`)

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		m := cmdPattern.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d: invalid command: %s", i+1, line)
		}

		cmd := BootCommand{
			Type: strings.ToLower(m[1]),
			Args: strings.TrimSpace(m[2]),
		}

		switch cmd.Type {
		case "wait", "delay":
			cmd.Type = "wait"
			if cmd.Args == "" {
				return nil, fmt.Errorf("line %d: wait requires a duration", i+1)
			}
			if _, err := time.ParseDuration(cmd.Args); err != nil {
				return nil, fmt.Errorf("line %d: invalid duration %q: %w", i+1, cmd.Args, err)
			}
		case "waitfortext":
			cmd.Type = "waitForText"
			cmd.Args = unquote(cmd.Args)
			if cmd.Args == "" {
				return nil, fmt.Errorf("line %d: waitForText requires text argument", i+1)
			}
		case "waitformenutext":
			cmd.Type = "waitForMenuText"
			cmd.Args = unquote(cmd.Args)
			if cmd.Args == "" {
				return nil, fmt.Errorf("line %d: waitForMenuText requires text argument", i+1)
			}
		case "click":
			cmd.Args = unquote(cmd.Args)
			if cmd.Args == "" {
				return nil, fmt.Errorf("line %d: click requires text argument", i+1)
			}
		case "clickmenu":
			cmd.Type = "clickMenu"
			cmd.Args = unquote(cmd.Args)
			if cmd.Args == "" {
				return nil, fmt.Errorf("line %d: clickMenu requires text argument", i+1)
			}
		case "clickmenuitem":
			cmd.Type = "clickMenuItem"
			cmd.Args = unquote(cmd.Args)
			menu, item := splitMenuItemArgs(cmd.Args)
			if menu == "" || item == "" {
				return nil, fmt.Errorf("line %d: clickMenuItem requires \"menu|item\"", i+1)
			}
		case "type":
			cmd.Args = unquote(cmd.Args)
			if cmd.Args == "" {
				return nil, fmt.Errorf("line %d: type requires text argument", i+1)
			}
		case "typeandreturniftext":
			cmd.Type = "typeAndReturnIfText"
			cmd.Args = unquote(cmd.Args)
			needle, value := splitConditionalTypeArgs(cmd.Args)
			if needle == "" || value == "" {
				return nil, fmt.Errorf("line %d: typeAndReturnIfText requires \"needle|value\"", i+1)
			}
		case "key":
			if cmd.Args == "" {
				return nil, fmt.Errorf("line %d: key requires key name", i+1)
			}
			if !isValidKeySpec(cmd.Args) {
				return nil, fmt.Errorf("line %d: invalid key spec %q", i+1, cmd.Args)
			}
		case "screenshot":
			// no args needed
		default:
			return nil, fmt.Errorf("line %d: unknown command %q", i+1, cmd.Type)
		}

		commands = append(commands, cmd)
	}

	return commands, nil
}

// BootCommandExecutor runs boot commands against a VM.
type BootCommandExecutor struct {
	ocr       *OCRService
	cs        *ControlServer
	verbose   bool
	debugDir  string // if set, save debug screenshots here
	stepDelay time.Duration
}

// NewBootCommandExecutor creates a new executor.
func NewBootCommandExecutor(ocr *OCRService, cs *ControlServer, verbose bool, debugDir string) *BootCommandExecutor {
	return &BootCommandExecutor{
		ocr:       ocr,
		cs:        cs,
		verbose:   verbose,
		debugDir:  debugDir,
		stepDelay: 500 * time.Millisecond,
	}
}

// Execute runs a sequence of boot commands.
func (e *BootCommandExecutor) Execute(commands []BootCommand) error {
	for i, cmd := range commands {
		if e.verbose {
			fmt.Printf("[boot] step %d/%d: <%s %s>\n", i+1, len(commands), cmd.Type, cmd.Args)
		}

		if err := e.executeOne(cmd); err != nil {
			return fmt.Errorf("step %d <%s %s>: %w", i+1, cmd.Type, cmd.Args, err)
		}

		time.Sleep(e.stepDelay)
	}
	return nil
}

func (e *BootCommandExecutor) executeOne(cmd BootCommand) error {
	switch cmd.Type {
	case "wait":
		d, _ := time.ParseDuration(cmd.Args) // already validated
		time.Sleep(d)
		return nil

	case "waitForText":
		return e.waitForText(cmd.Args, 60*time.Second)

	case "waitForMenuText":
		return e.waitForTextWithOptions(cmd.Args, 60*time.Second, OCRMenuSearchOptions())

	case "click":
		return e.clickText(cmd.Args, 60*time.Second)

	case "clickMenu":
		return e.clickTextWithOptions(cmd.Args, 60*time.Second, OCRMenuSearchOptions())

	case "clickMenuItem":
		menu, item := splitMenuItemArgs(cmd.Args)
		if menu == "" || item == "" {
			return fmt.Errorf("clickMenuItem requires \"menu|item\"")
		}
		return e.clickMenuItem(menu, item, 60*time.Second)

	case "type":
		return e.typeText(cmd.Args)

	case "typeAndReturnIfText":
		needle, value := splitConditionalTypeArgs(cmd.Args)
		if needle == "" || value == "" {
			return fmt.Errorf("typeAndReturnIfText requires \"needle|value\"")
		}
		return e.typeAndReturnIfText(needle, value, 8*time.Second)

	case "key":
		return e.sendKey(cmd.Args)

	case "screenshot":
		return e.saveScreenshot()

	default:
		return fmt.Errorf("unknown command: %s", cmd.Type)
	}
}

func (e *BootCommandExecutor) waitForText(text string, timeout time.Duration) error {
	return e.waitForTextWithOptions(text, timeout, OCRSearchOptions{})
}

func (e *BootCommandExecutor) waitForTextWithOptions(text string, timeout time.Duration, opts OCRSearchOptions) error {
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

func (e *BootCommandExecutor) clickText(text string, timeout time.Duration) error {
	return e.clickTextWithOptions(text, timeout, OCRSearchOptions{})
}

func (e *BootCommandExecutor) clickTextWithOptions(text string, timeout time.Duration, opts OCRSearchOptions) error {
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

func (e *BootCommandExecutor) clickMenuItem(menu, item string, timeout time.Duration) error {
	opts := OCRMenuSearchOptions()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := e.waitForTextWithOptions(menu, 3*time.Second, opts); err != nil {
			continue
		}
		if err := e.clickTextWithOptions(menu, 2*time.Second, opts); err != nil {
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
				return e.clickAt(x, y, img.Bounds().Dx(), img.Bounds().Dy())
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	return fmt.Errorf("timeout clicking menu item %q from menu %q", item, menu)
}

func (e *BootCommandExecutor) typeText(text string) error {
	resp := e.cs.typeText(&controlpb.TextCommand{Text: text})
	if !resp.Success {
		return fmt.Errorf("type text: %s", resp.Error)
	}
	return nil
}

func (e *BootCommandExecutor) typeAndReturnIfText(needle, value string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img := e.captureScreen()
		if img == nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		_, _, found := e.ocr.FindText(img, needle)
		if found {
			if err := e.typeText(value); err != nil {
				return err
			}
			return e.sendKey("return")
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func (e *BootCommandExecutor) sendKey(keySpec string) error {
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

func (e *BootCommandExecutor) clickAt(x, y float64, screenWidth, screenHeight int) error {
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

func (e *BootCommandExecutor) captureScreen() image.Image {
	if e.cs == nil {
		return nil
	}
	img, errMsg := e.cs.captureDisplayImage()
	if errMsg != "" {
		return nil
	}
	return img
}

func (e *BootCommandExecutor) saveScreenshot() error {
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
