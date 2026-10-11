// blocks.go - Objective-C block support for completion handlers

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/x/vzkit"
)

// vzDebugInstall is set by VZ_DEBUG_INSTALL=1 environment variable
var vzDebugInstall = os.Getenv("VZ_DEBUG_INSTALL") != ""

type nsErrorSnapshot struct {
	domain      string
	code        int
	description string
	reason      string
	underlying  *nsErrorSnapshot
}

func (e nsErrorSnapshot) Unwrap() error {
	if e.underlying == nil {
		return nil
	}
	return *e.underlying
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
	if e.underlying != nil {
		parts = append(parts, "underlying: "+e.underlying.Error())
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
		current := *nsErr
		return snapshotNSErrorChain(func() (uintptr, nsErrorSnapshot, bool) {
			if current.ID == 0 {
				return 0, nsErrorSnapshot{}, false
			}
			id := uintptr(current.ID)
			entry := nsErrorSnapshot{domain: current.Domain(), code: current.Code(), description: current.LocalizedDescription(), reason: current.LocalizedFailureReason()}
			info := current.UserInfo()
			current = foundation.NSError{}
			if info != nil {
				next := info.ObjectForKey(foundation.NewStringWithString("NSUnderlyingError"))
				if next != nil && objc.Send[bool](next.GetID(), objc.Sel("isKindOfClass:"), foundation.GetNSErrorClass().Class()) {
					current = foundation.NSErrorFromID(next.GetID())
				}
			}
			return id, entry, true
		})
	}
	return errors.New(err.Error())
}

func snapshotNSErrorChain(next func() (uintptr, nsErrorSnapshot, bool)) nsErrorSnapshot {
	var root nsErrorSnapshot
	tail := &root
	seen := make(map[uintptr]bool)
	for depth := 0; depth < 4; depth++ {
		id, entry, ok := next()
		if !ok || seen[id] {
			break
		}
		seen[id] = true
		entry.domain = boundedNSErrorText(entry.domain)
		entry.description = boundedNSErrorText(entry.description)
		entry.reason = boundedNSErrorText(entry.reason)
		entry.underlying = nil
		if depth == 0 {
			*tail = entry
		} else {
			tail.underlying = &entry
			tail = &entry
		}
	}
	return root
}

func boundedNSErrorText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1024 {
		end := 1024
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		return value[:end] + "..."
	}
	return value
}

func isVZAlreadyStoppedStopError(err error) bool {
	var snap nsErrorSnapshot
	if !errors.As(err, &snap) {
		return false
	}
	if !strings.EqualFold(snap.domain, "VZErrorDomain") || snap.code != 4 {
		return false
	}
	msg := strings.ToLower(snap.Error())
	return strings.Contains(msg, "stopped") && strings.Contains(msg, "stopping")
}

// isVZCannotStopError reports whether err is an invalid state transition error
// resulting from attempting to stop a VM that is already stopped, in an error state,
// or otherwise unable to transition to stopping.
func isVZCannotStopError(err error) bool {
	var snap nsErrorSnapshot
	if !errors.As(err, &snap) {
		return false
	}
	if !strings.EqualFold(snap.domain, "VZErrorDomain") || snap.code != 4 {
		return false
	}
	desc := strings.ToLower(snap.description + " " + snap.reason)
	if !strings.Contains(desc, "stopping") {
		return false
	}
	return strings.Contains(desc, "stopped") || strings.Contains(desc, `"error"`) || strings.Contains(desc, "“error”")
}

// isVZStorageAttachmentError reports whether err is the VZErrorDomain
// code=2 configuration failure raised when the disk image is already open
// by another process ("the storage device attachment is invalid", or the
// sibling "directory sharing device configuration is invalid").
func isVZStorageAttachmentError(err error) bool {
	var snap nsErrorSnapshot
	if !errors.As(err, &snap) {
		return false
	}
	if !strings.EqualFold(snap.domain, "VZErrorDomain") || snap.code != 2 {
		return false
	}
	msg := strings.ToLower(snap.Error())
	return strings.Contains(msg, "storage device attachment") ||
		strings.Contains(msg, "directory sharing device")
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

// DispatchSync executes a block synchronously on a raw dispatch queue handle.
func DispatchSync(queue uintptr, fn func()) {
	dispatch.QueueFromHandle(queue).Sync(fn)
}

// DispatchAsyncQueue schedules a block to run on a dispatch.Queue.
func DispatchAsyncQueue(queue dispatch.Queue, fn func()) {
	queue.Async(fn)
}

var dispatchAsyncMainFn = func(fn func()) {
	dispatch.MainQueue().Async(fn)
}

// DispatchAsyncMain schedules a block to run on the main dispatch queue.
func DispatchAsyncMain(fn func()) {
	if fn == nil {
		return
	}
	dispatchAsyncMainFn(fn)
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
