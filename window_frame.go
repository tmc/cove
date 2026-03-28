package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/foundation"
)

const windowFrameAutosavePrefix = "com.github.tmc.vz-macos.window."

// configureWindowFramePersistence restores a previously saved window frame (if
// present) and enables automatic frame persistence for subsequent moves/resizes.
func configureWindowFramePersistence(window appkit.NSWindow) (restored bool, name appkit.NSWindowFrameAutosaveName) {
	name = appkit.NSWindowFrameAutosaveName(windowFrameAutosaveNameForVM(vmName, vmDir, linuxMode))
	restored = window.SetFrameUsingName(name)
	if restoreWindowDisplayPlacement(window, name) {
		restored = true
	}
	window.SetFrameAutosaveName(name)
	return restored, name
}

type windowDisplayPlacement struct {
	DisplayID uint32 `json:"display_id"`
}

func windowFrameAutosaveNameForVM(name, dir string, isLinux bool) string {
	vmID := strings.TrimSpace(name)
	if vmID == "" {
		vmID = filepath.Base(strings.TrimSpace(dir))
	}
	if vmID == "" || vmID == "." || vmID == string(filepath.Separator) {
		vmID = "default"
	}
	osID := "macos"
	if isLinux {
		osID = "linux"
	}
	return windowFrameAutosavePrefix + sanitizeAutosaveToken(osID) + "." + sanitizeAutosaveToken(vmID)
}

func sanitizeAutosaveToken(s string) string {
	if strings.TrimSpace(s) == "" {
		return "default"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "default"
	}
	return out
}

func windowDisplayPlacementPath(name appkit.NSWindowFrameAutosaveName) string {
	dir := strings.TrimSpace(vmDir)
	if dir == "" {
		dir = "."
	}
	file := "window-display-" + sanitizeAutosaveToken(string(name)) + ".json"
	return filepath.Join(dir, file)
}

func saveWindowDisplayPlacement(window appkit.NSWindow, name appkit.NSWindowFrameAutosaveName) {
	placement := windowDisplayPlacement{DisplayID: screenDisplayID(window.Screen())}
	if placement.DisplayID == 0 {
		return
	}
	path := windowDisplayPlacementPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		if verbose {
			fmt.Printf("warning: create window placement dir: %v\n", err)
		}
		return
	}
	data, err := json.MarshalIndent(placement, "", "  ")
	if err != nil {
		if verbose {
			fmt.Printf("warning: encode window placement: %v\n", err)
		}
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil && verbose {
		fmt.Printf("warning: write window placement: %v\n", err)
	}
}

func restoreWindowDisplayPlacement(window appkit.NSWindow, name appkit.NSWindowFrameAutosaveName) bool {
	placement, ok := loadWindowDisplayPlacement(name)
	if !ok || placement.DisplayID == 0 {
		return false
	}
	target, ok := findScreenByDisplayID(placement.DisplayID)
	if !ok {
		return false
	}
	current := window.Screen()
	if current == nil || current.GetID() == 0 {
		current = appkit.GetNSScreenClass().MainScreen()
	}
	if current == nil || current.GetID() == 0 {
		return false
	}
	currentID := screenDisplayID(current)
	if currentID == placement.DisplayID {
		return true
	}
	frame := window.Frame()
	translated := translateWindowFrameBetweenScreens(frame, current.Frame(), target.Frame())
	constrained := window.ConstrainFrameRectToScreen(translated, target)
	window.SetFrameDisplay(constrained, true)
	return true
}

func loadWindowDisplayPlacement(name appkit.NSWindowFrameAutosaveName) (windowDisplayPlacement, bool) {
	path := windowDisplayPlacementPath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return windowDisplayPlacement{}, false
	}
	var placement windowDisplayPlacement
	if err := json.Unmarshal(data, &placement); err != nil {
		if verbose {
			fmt.Printf("warning: parse window placement %s: %v\n", path, err)
		}
		return windowDisplayPlacement{}, false
	}
	return placement, true
}

func findScreenByDisplayID(displayID uint32) (appkit.NSScreen, bool) {
	for _, screen := range appkit.GetNSScreenClass().Screens() {
		if screen.GetID() == 0 {
			continue
		}
		if screenDisplayID(screen) == displayID {
			return screen, true
		}
	}
	return appkit.NSScreen{}, false
}

func screenDisplayID(screen appkit.INSScreen) uint32 {
	if screen == nil || screen.GetID() == 0 {
		return 0
	}
	desc := screen.DeviceDescription()
	if desc.GetID() == 0 {
		return 0
	}
	value := desc.ObjectForKey(foundation.NewStringWithString("NSScreenNumber"))
	if value == nil || value.GetID() == 0 {
		return 0
	}
	return foundation.NSNumberFromID(value.GetID()).UnsignedIntValue()
}

func translateWindowFrameBetweenScreens(frame, fromScreen, toScreen corefoundation.CGRect) corefoundation.CGRect {
	dx := frame.Origin.X - fromScreen.Origin.X
	dy := frame.Origin.Y - fromScreen.Origin.Y
	frame.Origin.X = toScreen.Origin.X + dx
	frame.Origin.Y = toScreen.Origin.Y + dy
	return frame
}
