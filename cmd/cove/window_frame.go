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
	"github.com/tmc/apple/objc"
)

const windowFrameAutosavePrefix = "com.tmc.cove.window."

// configureWindowFramePersistence restores a previously saved window frame (if
// present) and enables automatic frame persistence for subsequent moves/resizes.
func configureWindowFramePersistence(window appkit.NSWindow) (restored bool, name appkit.NSWindowFrameAutosaveName) {
	osID := "macos"
	if linuxMode {
		osID = "linux"
	}
	return configureWindowFramePersistenceForVM(window, vmName, vmDir, osID)
}

func configureWindowFramePersistenceForVM(window appkit.NSWindow, vmName, vmDir, osID string) (restored bool, name appkit.NSWindowFrameAutosaveName) {
	name = appkit.NSWindowFrameAutosaveName(windowFrameAutosaveNameForVMOS(vmName, vmDir, osID))
	restored = window.SetFrameUsingName(name)
	if restoreWindowDisplayPlacementForDir(window, name, vmDir) {
		restored = true
	}
	objc.Send[struct{}](window.ID, objc.Sel("setFrameAutosaveName:"), objc.String(string(name)))
	return restored, name
}

type windowDisplayPlacement struct {
	DisplayID uint32 `json:"display_id"`
}

func windowFrameAutosaveNameForVM(name, dir string, isLinux bool) string {
	osID := "macos"
	if isLinux {
		osID = "linux"
	}
	return windowFrameAutosaveNameForVMOS(name, dir, osID)
}

func windowFrameAutosaveNameForVMOS(name, dir, osID string) string {
	vmID := strings.TrimSpace(name)
	if vmID == "" {
		vmID = filepath.Base(strings.TrimSpace(dir))
	}
	if vmID == "" || vmID == "." || vmID == string(filepath.Separator) {
		vmID = "default"
	}
	if strings.TrimSpace(osID) == "" {
		osID = "macos"
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
	return windowDisplayPlacementPathForDir(name, vmDir)
}

func windowDisplayPlacementPathForDir(name appkit.NSWindowFrameAutosaveName, dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "."
	}
	file := "window-display-" + sanitizeAutosaveToken(string(name)) + ".json"
	return filepath.Join(dir, file)
}

func saveWindowDisplayPlacement(window appkit.NSWindow, name appkit.NSWindowFrameAutosaveName) {
	saveWindowDisplayPlacementForDir(window, name, vmDir)
}

func saveWindowDisplayPlacementForDir(window appkit.NSWindow, name appkit.NSWindowFrameAutosaveName, dir string) {
	placement := windowDisplayPlacement{DisplayID: screenDisplayID(window.Screen())}
	if placement.DisplayID == 0 {
		return
	}
	path := windowDisplayPlacementPathForDir(name, dir)
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
	return restoreWindowDisplayPlacementForDir(window, name, vmDir)
}

func restoreWindowDisplayPlacementForDir(window appkit.NSWindow, name appkit.NSWindowFrameAutosaveName, dir string) bool {
	placement, ok := loadWindowDisplayPlacementForDir(name, dir)
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
	return loadWindowDisplayPlacementForDir(name, vmDir)
}

func loadWindowDisplayPlacementForDir(name appkit.NSWindowFrameAutosaveName, dir string) (windowDisplayPlacement, bool) {
	path := windowDisplayPlacementPathForDir(name, dir)
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
