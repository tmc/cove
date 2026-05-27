//go:build darwin

// macgo integration for cove.
// macgo integration for cove.
// macgo remains opt-in until the bundled launch path stops failing in the
// current purego AppKit runtime. Helper and runtime commands stay on the plain
// autosign path by default.

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tmc/cove/internal/assets"
	"github.com/tmc/cove/internal/autosign"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/macgo"
)

const (
	vzmacEnableMacgoEnv    = "VZMAC_ENABLE_MACGO"
	coveAppSandboxMacgoEnv = "COVE_APP_SANDBOX_MACGO"
)

// initMacgo ensures entitlements and optionally enables macgo for headed UI
// experiments. Helper and runtime commands stay on the plain autosign path by
// default.
func initMacgo() {
	inAppBundle := runningInAppBundle()
	if !inAppBundle {
		if err := autosign.EnsureEntitlements(); err != nil {
			fmt.Fprintf(os.Stderr, "autosign: %v\n", err)
		}
	}
	if inAppBundle && appSandboxMacgoEnabled() {
		return
	}
	if !shouldEnableMacgo(flag.Args(), guiMode, headlessMode, runVM, installVM, utmBundlePath) {
		return
	}

	iconPath, err := assets.WriteIconToTemp()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write icon: %v\n", err)
	} else {
		defer os.Remove(iconPath)
	}

	cfg := macgo.NewConfig()
	cfg.AppName = "cove"
	cfg.BundleID = "com.tmc.cove"
	cfg.WithCustom(
		"com.apple.security.virtualization",
		"com.apple.security.network.client",
		"com.apple.security.network.server",
	)
	if appSandboxMacgoEnabled() {
		cfg.WithPermissions(macgo.Sandbox)
		cfg.WithCustom(
			"com.apple.security.files.bookmarks.app-scope",
			"com.apple.security.files.user-selected.read-write",
		)
		if stateDir := os.Getenv(vmconfig.StateDirEnv); stateDir != "" {
			cfg.WithEnvironment(vmconfig.StateDirEnv, stateDir)
		}
		cfg.WithAdHocSign()
	}
	cfg.WithPostCreateHook(func(bundlePath string, cfg *macgo.Config) error {
		return installCoveVMDocumentTypes(bundlePath)
	})

	if iconPath != "" {
		cfg.WithIcon(iconPath)
	}

	cfg.WithUIMode(desiredMacgoUIMode(flag.Args(), guiMode, headlessMode, runVM, installVM, utmBundlePath))

	if os.Getenv("MACGO_DEBUG") == "1" || verbose {
		cfg.WithDebug()
	}

	if err := macgo.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "macgo: %v\n", err)
	}
}

func shouldEnableMacgo(args []string, gui, headless, legacyRun, legacyInstall bool, utmPath string) bool {
	if os.Getenv("VZMAC_NO_MACGO") == "1" {
		return false
	}
	if appSandboxMacgoEnabled() {
		return true
	}
	if os.Getenv(vzmacEnableMacgoEnv) != "1" {
		return false
	}
	return wantsRegularUIMode(args, gui, headless, legacyRun, legacyInstall, utmPath)
}

func appSandboxMacgoEnabled() bool {
	return os.Getenv(coveAppSandboxMacgoEnv) == "1"
}

func runningInAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

func wantsMacgoRuntime(args []string, legacyRun, legacyInstall bool, utmPath string) bool {
	if legacyRun || legacyInstall || utmPath != "" {
		return true
	}
	if _, ok := coveVMBundlePathArg(args); ok {
		return true
	}
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "run", "install", "up":
		return true
	default:
		return false
	}
}

func wantsRegularUIMode(args []string, gui, headless, legacyRun, legacyInstall bool, utmPath string) bool {
	if !wantsMacgoRuntime(args, legacyRun, legacyInstall, utmPath) {
		return false
	}
	if headless || !gui {
		return false
	}
	if flagValue, ok := explicitBoolFlag(args, "headless"); ok && flagValue {
		return false
	}
	if flagValue, ok := explicitBoolFlag(args, "gui"); ok {
		return flagValue
	}
	return true
}

func desiredMacgoUIMode(args []string, gui, headless, legacyRun, legacyInstall bool, utmPath string) macgo.UIMode {
	if appSandboxMacgoEnabled() {
		return macgo.UIModeAccessory
	}
	if wantsRegularUIMode(args, gui, headless, legacyRun, legacyInstall, utmPath) {
		return macgo.UIModeRegular
	}
	return macgo.UIModeAccessory
}

func explicitBoolFlag(args []string, name string) (bool, bool) {
	short := "-" + name
	long := "--" + name
	for _, arg := range args {
		switch arg {
		case short, long:
			return true, true
		case short + "=true", long + "=true":
			return true, true
		case short + "=false", long + "=false":
			return false, true
		}
	}
	return false, false
}
