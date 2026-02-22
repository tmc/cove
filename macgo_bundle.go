//go:build darwin

// macgo integration for vz-macos.
// Uses single-process mode: codesign + setActivationPolicy.
// No .app bundle, no child process, no I/O forwarding.
package main

import (
	"fmt"
	"os"
	"slices"

	"github.com/tmc/macgo"
	"github.com/tmc/vz-macos/internal/assets"
	"github.com/tmc/vz-macos/internal/autosign"
)

// initMacgo sets up macgo with single-process mode.
// Falls back to autosign if macgo fails.
func initMacgo() {
	if os.Getenv("VZMAC_NO_MACGO") == "1" {
		if err := autosign.EnsureEntitlements(); err != nil {
			fmt.Fprintf(os.Stderr, "autosign: %v\n", err)
		}
		return
	}

	iconPath, err := assets.WriteIconToTemp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write icon: %v\n", err)
	} else {
		defer os.Remove(iconPath)
	}

	cfg := macgo.NewConfig()
	cfg.AppName = "vz-macos"
	cfg.BundleID = "com.tmc.vz-macos"
	cfg.WithSingleProcess()
	cfg.WithCustom(
		"com.apple.security.virtualization",
		"com.apple.security.network.client",
		"com.apple.security.network.server",
	)

	if iconPath != "" {
		cfg.WithIcon(iconPath)
	}

	noSubcommand := len(os.Args) == 1
	wantGUI := guiMode || slices.Contains(os.Args[1:], "-gui") || noSubcommand
	if wantGUI {
		cfg.WithUIMode(macgo.UIModeRegular)
	} else {
		cfg.WithUIMode(macgo.UIModeBackground)
	}

	if os.Getenv("MACGO_DEBUG") == "1" || verbose {
		cfg.WithDebug()
	}

	if err := macgo.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "macgo: %v (falling back to autosign)\n", err)
		if err := autosign.EnsureEntitlements(); err != nil {
			fmt.Fprintf(os.Stderr, "autosign: %v\n", err)
		}
	}
}
