// blocks.go - Objective-C block support for completion handlers
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/utils"
	"github.com/tmc/appledocs/generated/dispatch"
	"github.com/tmc/appledocs/generated/foundation"
	"github.com/tmc/appledocs/generated/objc"
	"github.com/tmc/vzkit"
)

// DISPATCH_TIME_FOREVER is the constant for waiting forever.
const DISPATCH_TIME_FOREVER = dispatch.TimeForever

// verboseLog is set by the -v flag to enable verbose logging
var verboseLog bool

// vzDebugInstall is set by VZ_DEBUG_INSTALL=1 environment variable
var vzDebugInstall = os.Getenv("VZ_DEBUG_INSTALL") != ""

// SetVerbose enables or disables verbose logging
func SetVerbose(v bool) {
	verboseLog = v
}

// vzlog prints install debug messages if VZ_DEBUG_INSTALL=1
func vzlog(format string, args ...interface{}) {
	if vzDebugInstall {
		fmt.Printf("[VZ-INSTALL-DEBUG] "+format+"\n", args...)
	}
}

// GetMainDispatchQueue returns the main dispatch queue handle.
func GetMainDispatchQueue() uintptr {
	return uintptr(dispatch.MainQueue().Handle())
}

// DispatchSync executes a block synchronously on a raw dispatch queue handle.
func DispatchSync(queue uintptr, fn func()) {
	dispatch.QueueFromHandle(queue).Sync(fn)
}

// DispatchAsync schedules a block to run on a raw dispatch queue handle.
func DispatchAsync(queue uintptr, fn func()) {
	dispatch.QueueFromHandle(queue).Async(fn)
}

// DispatchAsyncQueue schedules a block to run on a dispatch.Queue.
func DispatchAsyncQueue(queue dispatch.Queue, fn func()) {
	queue.Async(fn)
}

// DispatchSyncQueue executes a block synchronously on a dispatch.Queue.
func DispatchSyncQueue(queue dispatch.Queue, fn func()) {
	queue.Sync(fn)
}

// runRunLoopOnce runs the main run loop briefly to process pending callbacks.
func runRunLoopOnce() {
	vzkit.RunRunLoopOnce()
}

// runRunLoopAggressively runs the run loop more aggressively for long-running operations.
func runRunLoopAggressively() {
	utils.RunRunLoopAggressively()
}

// printUnderlyingErrorDetails prints detailed information about an NSError and its underlying errors.
func printUnderlyingErrorDetails(nsError objc.ID) {
	vzkit.PrintNSErrorDetailed(nsError)
}

// ErrorCompletionHandler captures an optional NSError from an Objective-C completion handler.
type ErrorCompletionHandler = vzkit.ErrorCompletionHandler

// printDetailedInstallError prints verbose error details for an installation failure.
// It type-asserts the error back to *foundation.NSError (since NSErrorToError preserves
// the type) and prints domain, code, failure reason, user info, and underlying errors.
// It also queries the system log for recent Virtualization subsystem messages.
func printDetailedInstallError(err error) {
	fmt.Printf("Installation failed: %v\n", err)

	var nsErr *foundation.NSError
	if errors.As(err, &nsErr) && nsErr.ID != 0 {
		fmt.Println()
		printUnderlyingErrorDetails(nsErr.ID)
	}

	// Query system log for recent Virtualization-related errors.
	printRecentVirtualizationLogs(2 * time.Minute)
}

// printRecentVirtualizationLogs queries the unified system log for recent
// Virtualization subsystem messages to help diagnose installation failures.
func printRecentVirtualizationLogs(window time.Duration) {
	mins := int(window.Minutes())
	if mins < 1 {
		mins = 1
	}
	predicate := `subsystem == "com.apple.Virtualization" OR process CONTAINS "AMRestoreAgent" OR process CONTAINS "MobileRestore" OR (process CONTAINS "mobileassetd" AND eventMessage CONTAINS "Restore")`
	cmd := exec.Command("log", "show",
		"--predicate", predicate,
		fmt.Sprintf("--last=%dm", mins),
		"--style=compact",
		"--info",
	)
	out, err := cmd.Output()
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	// Filter to error/fault level lines and important keywords.
	var relevant []string
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") ||
			strings.Contains(lower, "fail") ||
			strings.Contains(lower, "broken pipe") ||
			strings.Contains(lower, "state machine") ||
			strings.Contains(lower, "dfu") ||
			strings.Contains(lower, "restoreos") ||
			strings.Contains(lower, "asr") ||
			strings.Contains(lower, "cferror") ||
			strings.Contains(lower, "ssl") {
			relevant = append(relevant, line)
		}
	}
	if len(relevant) == 0 {
		return
	}
	fmt.Printf("\n=== Recent System Log (Virtualization/Restore, last %dm) ===\n", mins)
	// Show at most 40 lines to keep output manageable.
	if len(relevant) > 40 {
		relevant = relevant[len(relevant)-40:]
		fmt.Println("  ... (showing last 40 relevant lines)")
	}
	for _, line := range relevant {
		fmt.Println("  " + line)
	}
	fmt.Println("=== End System Log ===")
}
