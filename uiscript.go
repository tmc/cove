// uiscript.go - rsc.io/script-based UI automation engine for VMs.
//
// Extends rsc.io/script with commands for OCR-driven GUI automation
// and keyboard/mouse input. Scripts are standard txtar archives executed
// by rsc.io/script.
//
// UI automation commands:
//
//	screenshot [file]           Capture VM screen; save to file or set stdout
//	ocr                        Run OCR on current screen; set stdout to all text
//	ocr-click <text>            Find text on screen via OCR and click its center
//	ocr-wait <text> [timeout]   Wait until text appears on screen
//	ocr-gone <text> [timeout]   Wait until text disappears from screen
//	type <text>                 Type text via clipboard paste (Cmd+V)
//	key <spec>                  Send key event (e.g. "return", "tab", "cmd+v")
//	click <x> <y>              Click at normalized coordinates (0-1, top-left)
//	wait <duration>             Sleep for a duration
//	detect-page                 Detect Setup Assistant page via OCR; set stdout
//	detect-screen               Detect screen state (desktop/login/setup); set stdout
//
// Conditions:
//
//	[screen:<state>]            True if current screen matches state
//	[page:<name>]               True if current SA page matches name
//	[text-visible:<text>]       True if text is currently visible on screen
//
// Example:
//
//	# Navigate Setup Assistant
//	ocr-wait "Continue" 30s
//	ocr-click "Continue"
//	wait 2s
//	ocr-wait "Country"
//	key return
//	wait 2s
//	ocr-click "Not Now"
//
//	# Type in a form field
//	ocr-click "Full Name"
//	type "Test User"
//	key tab
//	type "testuser"
package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
	"rsc.io/script"
)

// uiscriptConfig holds configuration for the UI automation engine.
type uiscriptConfig struct {
	cs      *ControlServer // in-process control server
	ocr     *OCRService
	verbose bool
	saveDir string // directory for debug screenshots
}

// newUIScriptEngine returns a script engine with UI automation commands.
func newUIScriptEngine(cfg uiscriptConfig) *script.Engine {
	defaults := script.DefaultCmds()
	cmds := map[string]script.Cmd{
		// UI automation commands
		"screenshot":    screenshotCmd(cfg),
		"ocr":           ocrCmd(cfg),
		"ocr-click":     ocrClickCmd(cfg),
		"ocr-wait":      ocrWaitCmd(cfg),
		"ocr-gone":      ocrGoneCmd(cfg),
		"type":          typeCmd(cfg),
		"key":           keyCmd(cfg),
		"click":         clickCmd(cfg),
		"wait":          waitCmd(),
		"detect-page":   detectPageCmd(cfg),
		"detect-screen": detectScreenCmd(cfg),

		// Standard commands
		"cat":    defaults["cat"],
		"echo":   defaults["echo"],
		"env":    defaults["env"],
		"sleep":  defaults["sleep"],
		"stdout": defaults["stdout"],
		"stderr": defaults["stderr"],
		"stop":   defaults["stop"],
		"help":   defaults["help"],
	}

	conds := script.DefaultConds()
	conds["screen"] = screenCond(cfg)
	conds["page"] = pageCond(cfg)
	conds["text-visible"] = textVisibleCond(cfg)

	return &script.Engine{
		Cmds:  cmds,
		Conds: conds,
	}
}

// screenshotCmd captures the VM screen.
// Usage: screenshot [file]
func screenshotCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "capture VM screen to file or stdout",
			Args:    "[file]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			img, errMsg := cfg.cs.captureVMView()
			if errMsg != "" {
				return nil, fmt.Errorf("capture: %s", errMsg)
			}

			filename := ""
			if len(args) > 0 {
				filename = args[0]
			} else if cfg.saveDir != "" {
				filename = filepath.Join(cfg.saveDir,
					fmt.Sprintf("uiscript_%d.png", time.Now().UnixMilli()))
			}

			if filename == "" {
				return func(*script.State) (string, string, error) {
					return "screenshot captured\n", "", nil
				}, nil
			}

			path := s.Path(filename)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return nil, fmt.Errorf("mkdir: %w", err)
			}
			f, err := os.Create(path)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			if err := png.Encode(f, img); err != nil {
				return nil, err
			}

			return func(*script.State) (string, string, error) {
				return path + "\n", "", nil
			}, nil
		},
	)
}

// ocrCmd runs OCR on the current screen.
// Usage: ocr
func ocrCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{Summary: "run OCR on current screen; stdout is all recognized text"},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			text, err := cfg.cs.OCRAllText(cfg.ocr)
			if err != nil {
				return nil, err
			}
			return func(*script.State) (string, string, error) {
				return text + "\n", "", nil
			}, nil
		},
	)
}

// ocrClickCmd finds text on screen and clicks it.
// Usage: ocr-click <text> [timeout]
func ocrClickCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "find text on screen via OCR and click its center",
			Args:    "text [timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			text := args[0]
			timeout := 10 * time.Second
			if len(args) > 1 {
				d, err := time.ParseDuration(args[1])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[1], err)
				}
				timeout = d
			}

			if cfg.verbose {
				s.Logf("ocr-click %q (timeout %s)\n", text, timeout)
			}
			if err := cfg.cs.OCRClickText(cfg.ocr, text, timeout); err != nil {
				return nil, err
			}
			return func(*script.State) (string, string, error) {
				return fmt.Sprintf("clicked %q\n", text), "", nil
			}, nil
		},
	)
}

// ocrWaitCmd waits for text to appear on screen.
// Usage: ocr-wait <text> [timeout]
func ocrWaitCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "wait for text to appear on screen via OCR",
			Args:    "text [timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			text := args[0]
			timeout := 60 * time.Second
			if len(args) > 1 {
				d, err := time.ParseDuration(args[1])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[1], err)
				}
				timeout = d
			}

			if cfg.verbose {
				s.Logf("ocr-wait %q (timeout %s)\n", text, timeout)
			}
			if err := cfg.cs.OCRWaitForText(cfg.ocr, text, timeout); err != nil {
				return nil, err
			}
			return func(*script.State) (string, string, error) {
				return fmt.Sprintf("found %q\n", text), "", nil
			}, nil
		},
	)
}

// ocrGoneCmd waits for text to disappear from screen.
// Usage: ocr-gone <text> [timeout]
func ocrGoneCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "wait for text to disappear from screen via OCR",
			Args:    "text [timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			text := args[0]
			timeout := 30 * time.Second
			if len(args) > 1 {
				d, err := time.ParseDuration(args[1])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[1], err)
				}
				timeout = d
			}

			deadline := time.Now().Add(timeout)
			for time.Now().Before(deadline) {
				img, errMsg := cfg.cs.captureVMView()
				if errMsg != "" {
					time.Sleep(time.Second)
					continue
				}
				_, _, found := cfg.ocr.FindTextNormalized(img, text)
				if !found {
					return func(*script.State) (string, string, error) {
						return fmt.Sprintf("%q gone\n", text), "", nil
					}, nil
				}
				time.Sleep(time.Second)
			}
			return nil, fmt.Errorf("timeout: text %q still visible", text)
		},
	)
}

// typeCmd types text via clipboard paste.
// Usage: type <text>
func typeCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "type text into the VM via clipboard paste",
			Args:    "text",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			text := strings.Join(args, " ")
			if cfg.verbose {
				s.Logf("type %q\n", text)
			}
			cfg.cs.pasteText(text)
			return nil, nil
		},
	)
}

// keyCmd sends a key event.
// Usage: key <spec>
// Spec: "return", "tab", "escape", "cmd+v", "shift+a", etc.
func keyCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "send a key event to the VM",
			Args:    "keyspec",
			Detail:  []string{"Examples: return, tab, escape, space, cmd+v, shift+a, cmd+shift+3"},
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			spec := args[0]
			if !isValidKeySpec(spec) {
				return nil, fmt.Errorf("invalid key spec %q", spec)
			}
			keyCode, modifiers := parseKeySpec(spec)
			if cfg.verbose {
				s.Logf("key %s (code=%d mods=%d)\n", spec, keyCode, modifiers)
			}

			resp := cfg.cs.sendKeyEvent(&controlpb.KeyCommand{
				KeyCode:    uint32(keyCode),
				KeyDown:    true,
				Modifiers:  uint32(modifiers),
				UseCgEvent: true,
			})
			if !resp.Success {
				return nil, fmt.Errorf("key down: %s", resp.Error)
			}
			time.Sleep(50 * time.Millisecond)
			resp = cfg.cs.sendKeyEvent(&controlpb.KeyCommand{
				KeyCode:    uint32(keyCode),
				KeyDown:    false,
				Modifiers:  uint32(modifiers),
				UseCgEvent: true,
			})
			if !resp.Success {
				return nil, fmt.Errorf("key up: %s", resp.Error)
			}
			return nil, nil
		},
	)
}

// clickCmd clicks at normalized coordinates.
// Usage: click <x> <y>
func clickCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "click at normalized coordinates (0-1, top-left origin)",
			Args:    "x y",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 2 {
				return nil, script.ErrUsage
			}
			var x, y float64
			if _, err := fmt.Sscanf(args[0], "%f", &x); err != nil {
				return nil, fmt.Errorf("invalid x %q: %w", args[0], err)
			}
			if _, err := fmt.Sscanf(args[1], "%f", &y); err != nil {
				return nil, fmt.Errorf("invalid y %q: %w", args[1], err)
			}
			if cfg.verbose {
				s.Logf("click (%.3f, %.3f)\n", x, y)
			}
			resp := cfg.cs.sendMouseEvent(&controlpb.MouseCommand{
				X: x, Y: y, Button: 0, Action: "click",
			})
			if !resp.Success {
				return nil, fmt.Errorf("click: %s", resp.Error)
			}
			return nil, nil
		},
	)
}

// waitCmd sleeps for a duration.
// Usage: wait <duration>
func waitCmd() script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "sleep for a duration",
			Args:    "duration",
			Detail:  []string{"Examples: 1s, 500ms, 2m"},
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			d, err := time.ParseDuration(args[0])
			if err != nil {
				return nil, fmt.Errorf("invalid duration %q: %w", args[0], err)
			}
			time.Sleep(d)
			return nil, nil
		},
	)
}

// detectPageCmd identifies the current Setup Assistant page.
// Usage: detect-page
func detectPageCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{Summary: "detect current Setup Assistant page via OCR"},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			page := cfg.cs.OCRDetectPage(cfg.ocr)
			return func(*script.State) (string, string, error) {
				return page + "\n", "", nil
			}, nil
		},
	)
}

// detectScreenCmd identifies the current screen state.
// Usage: detect-screen
func detectScreenCmd(cfg uiscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{Summary: "detect screen state (desktop, login, setup-assistant, etc.)"},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			img, errMsg := cfg.cs.captureVMView()
			if errMsg != "" {
				return nil, fmt.Errorf("capture: %s", errMsg)
			}
			state := DetectScreenStateOCR(img, cfg.ocr)
			return func(*script.State) (string, string, error) {
				return state.String() + "\n", "", nil
			}, nil
		},
	)
}

// screenCond is a prefix condition: [screen:desktop], [screen:login], etc.
func screenCond(cfg uiscriptConfig) script.Cond {
	return script.PrefixCondition(
		"current screen state matches",
		func(s *script.State, suffix string) (bool, error) {
			img, errMsg := cfg.cs.captureVMView()
			if errMsg != "" {
				return false, nil
			}
			state := DetectScreenStateOCR(img, cfg.ocr)
			return state.String() == suffix, nil
		},
	)
}

// pageCond is a prefix condition: [page:language], [page:user_account], etc.
func pageCond(cfg uiscriptConfig) script.Cond {
	return script.PrefixCondition(
		"current Setup Assistant page matches",
		func(s *script.State, suffix string) (bool, error) {
			page := cfg.cs.OCRDetectPage(cfg.ocr)
			return page == suffix, nil
		},
	)
}

// textVisibleCond is a prefix condition: [text-visible:Continue]
func textVisibleCond(cfg uiscriptConfig) script.Cond {
	return script.PrefixCondition(
		"text is visible on screen",
		func(s *script.State, suffix string) (bool, error) {
			img, errMsg := cfg.cs.captureVMView()
			if errMsg != "" {
				return false, nil
			}
			_, _, found := cfg.ocr.FindTextNormalized(img, suffix)
			return found, nil
		},
	)
}
