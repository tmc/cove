//go:build darwin

// UTM Launcher helpers for cove.
// Provides native NSOpenPanel file picker.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/foundation"
)

// showOpenPanelForUTM displays an NSOpenPanel for selecting a .utm bundle.
// Returns the selected path or empty string if cancelled.
func showOpenPanelForUTM() string {
	// Must run on main thread for GUI
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Initialize app if needed (for panel to work)
	app := getSharedApp()
	app.SetActivationPolicy(appkit.NSApplicationActivationPolicyRegular)
	app.Activate() // Bring to front

	// Create open panel
	panel := appkit.NewNSOpenPanel()
	panel.SetTitle("Select UTM VM Bundle")
	panel.SetMessage("Choose a .utm bundle to run")
	panel.SetCanChooseFiles(true)       // .utm bundles appear as files (packages)
	panel.SetCanChooseDirectories(true) // Also allow directory selection
	panel.SetAllowsMultipleSelection(false)
	panel.SetPrompt("Select")
	panel.SetTreatsFilePackagesAsDirectories(false) // Treat .utm as opaque packages

	// Set default directory - try UTM container first, fall back to home
	home, _ := os.UserHomeDir()
	defaultPaths := []string{
		filepath.Join(home, "Library/Containers/com.utmapp.UTM/Data/Documents"),
		filepath.Join(home, "Library/Containers/com.utmapp.UTM-SE/Data/Documents"),
		filepath.Join(home, "Documents"),
		home,
	}
	for _, p := range defaultPaths {
		if _, err := os.Stat(p); err == nil {
			url := foundation.NewURLFileURLWithPath(p)
			panel.SetDirectoryURL(url)
			break
		}
	}

	// Run the panel modally
	response := panel.RunModal()
	// NSModalResponseOK = 1
	const NSModalResponseOK appkit.NSModalResponse = 1
	if response == NSModalResponseOK {
		// Use URL() from SavePanel (single selection) instead of URLs()
		url := panel.URL()
		if url.GetID() != 0 {
			return url.Path()
		}
		return ""
	}
	// User cancelled (response != OK)
	return ""
}

// runUTMLauncherGUI shows a GUI file picker and launches the selected VM.
// Used when -gui -utm flags are both set.
func runUTMLauncherGUI() error {
	path := showOpenPanelForUTM()
	if path == "" {
		return nil // User cancelled
	}
	if !strings.HasSuffix(path, ".utm") {
		// Show alert for invalid selection
		showAlert("Invalid Selection", "Please select a .utm bundle")
		return runUTMLauncherGUI() // Try again
	}
	return runUTMBundle(path)
}

// showAlert displays a simple alert dialog.
func showAlert(title, message string) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	app := getSharedApp()
	app.SetActivationPolicy(appkit.NSApplicationActivationPolicyRegular)
	app.Activate()

	alert := appkit.NewNSAlert()
	alert.SetMessageText(title)
	alert.SetInformativeText(message)
	alert.AddButtonWithTitle("OK")
	alert.RunModal()
}
