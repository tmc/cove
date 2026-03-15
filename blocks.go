// blocks.go - Objective-C block support for completion handlers
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/x/vzkit"
)

// vzDebugInstall is set by VZ_DEBUG_INSTALL=1 environment variable
var vzDebugInstall = os.Getenv("VZ_DEBUG_INSTALL") != ""

type nsErrorSnapshot struct {
	domain      string
	code        int
	description string
	reason      string
}

func (e nsErrorSnapshot) Error() string {
	var parts []string
	if e.domain != "" {
		parts = append(parts, fmt.Sprintf("domain=%s code=%d", e.domain, e.code))
	}
	if e.description != "" {
		parts = append(parts, e.description)
	}
	if e.reason != "" && e.reason != e.description {
		parts = append(parts, e.reason)
	}
	if len(parts) == 0 {
		return "virtualization error"
	}
	return strings.Join(parts, ": ")
}

func snapshotNSError(err error) error {
	if err == nil {
		return nil
	}
	var nsErr *foundation.NSError
	if errors.As(err, &nsErr) && nsErr != nil && nsErr.ID != 0 {
		return nsErrorSnapshot{
			domain:      nsErr.Domain(),
			code:        nsErr.Code(),
			description: strings.TrimSpace(nsErr.LocalizedDescription()),
			reason:      strings.TrimSpace(nsErr.LocalizedFailureReason()),
		}
	}
	return errors.New(err.Error())
}

func printNSErrorSummary(prefix string, err error) bool {
	switch e := err.(type) {
	case nsErrorSnapshot:
		if e.domain != "" {
			fmt.Printf("%s: domain=%s code=%d\n", prefix, e.domain, e.code)
		}
		if e.description != "" {
			fmt.Printf("%s: %s\n", prefix, e.description)
		}
		if e.reason != "" && e.reason != e.description {
			fmt.Printf("%s failure reason: %s\n", prefix, e.reason)
		}
		return true
	case *foundation.NSError:
		if e != nil && e.ID != 0 {
			fmt.Printf("%s: domain=%s code=%d\n", prefix, e.Domain(), e.Code())
			fmt.Printf("%s: %s\n", prefix, e.LocalizedDescription())
			if reason := e.LocalizedFailureReason(); reason != "" {
				fmt.Printf("%s failure reason: %s\n", prefix, reason)
			}
			return true
		}
	}
	return false
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

// printDetailedInstallError prints verbose error details for an installation failure.
// It type-asserts the error back to *foundation.NSError (since NSErrorToError preserves
// the type) and prints domain, code, failure reason, user info, and underlying errors.
// It also queries the system log for recent Virtualization subsystem messages.
func printDetailedInstallError(err error) {
	fmt.Printf("Installation failed: %v\n", err)

	var nsErr *foundation.NSError
	if errors.As(err, &nsErr) && nsErr.ID != 0 {
		fmt.Println()
		vzkit.PrintNSErrorDetailed(nsErr.ID)
	} else if snap, ok := err.(nsErrorSnapshot); ok {
		fmt.Println()
		fmt.Printf("NSError domain=%s code=%d\n", snap.domain, snap.code)
		if snap.description != "" {
			fmt.Printf("Description: %s\n", snap.description)
		}
		if snap.reason != "" && snap.reason != snap.description {
			fmt.Printf("Failure reason: %s\n", snap.reason)
		}
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
