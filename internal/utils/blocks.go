// blocks.go - Objective-C block support for completion handlers
package utils

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/appledocs/generated/appkit"
	"github.com/tmc/appledocs/generated/dispatch"
	"github.com/tmc/appledocs/generated/foundation"
	"github.com/tmc/appledocs/generated/objc"
)

// DISPATCH_TIME_FOREVER is the constant for waiting forever.
const DISPATCH_TIME_FOREVER = dispatch.TimeForever

// CFRunLoop functions (exported for use by root package event loops).
var (
	CFRunLoopRunInMode    func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32
	cfRunLoopGetMain      func() uintptr
	cfRunLoopGetCurrent   func() uintptr
	cfRunLoopRun          func()
	cfRunLoopWakeUp       func(rl uintptr)
	KCFRunLoopDefaultMode uintptr
)

func init() {
	cfLib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		panic(err)
	}
	purego.RegisterLibFunc(&CFRunLoopRunInMode, cfLib, "CFRunLoopRunInMode")
	purego.RegisterLibFunc(&cfRunLoopGetMain, cfLib, "CFRunLoopGetMain")
	purego.RegisterLibFunc(&cfRunLoopGetCurrent, cfLib, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&cfRunLoopRun, cfLib, "CFRunLoopRun")
	purego.RegisterLibFunc(&cfRunLoopWakeUp, cfLib, "CFRunLoopWakeUp")

	KCFRunLoopDefaultMode, err = purego.Dlsym(cfLib, "kCFRunLoopDefaultMode")
	if err != nil {
		panic(err)
	}
	// KCFRunLoopDefaultMode is a pointer to CFStringRef, dereference it
	KCFRunLoopDefaultMode = *(*uintptr)(unsafe.Pointer(KCFRunLoopDefaultMode))
}

// verboseLog is set by the -v flag to enable verbose logging
var verboseLog bool

// vzDebugInstall is set by VZ_DEBUG_INSTALL=1 environment variable
var vzDebugInstall = os.Getenv("VZ_DEBUG_INSTALL") != ""

// vlog prints a message if verbose mode is enabled
func vlog(format string, args ...interface{}) {
	if verboseLog {
		fmt.Printf("[runloop] "+format+"\n", args...)
	}
}

// GetMainDispatchQueue returns the main dispatch queue handle.
func GetMainDispatchQueue() uintptr {
	return uintptr(dispatch.MainQueue().Handle())
}

// CFRunLoop return values
const (
	kCFRunLoopRunFinished      = 1 // Run loop finished - no sources or timers
	kCFRunLoopRunStopped       = 2 // Run loop was stopped with CFRunLoopStop
	kCFRunLoopRunTimedOut      = 3 // Run loop timed out
	kCFRunLoopRunHandledSource = 4 // Run loop handled a source
)

func runLoopResultString(result int32) string {
	switch result {
	case kCFRunLoopRunFinished:
		return "Finished (no sources)"
	case kCFRunLoopRunStopped:
		return "Stopped"
	case kCFRunLoopRunTimedOut:
		return "TimedOut"
	case kCFRunLoopRunHandledSource:
		return "HandledSource"
	default:
		return fmt.Sprintf("Unknown(%d)", result)
	}
}

// RunRunLoopAggressively runs the run loop more aggressively for long-running operations.
// This pumps both CFRunLoop and NSRunLoop multiple times to ensure all pending work is processed.
func RunRunLoopAggressively() {
	vlog("=== runRunLoopAggressively START ===")

	// Get run loop info
	mainRL := cfRunLoopGetMain()
	currentRL := cfRunLoopGetCurrent()
	vlog("  CFRunLoopGetMain: %#x, CFRunLoopGetCurrent: %#x, same=%v", mainRL, currentRL, mainRL == currentRL)
	vlog("  dispatch_main_queue: %#x", GetMainDispatchQueue())

	// Try waking up the main run loop
	if mainRL != 0 {
		cfRunLoopWakeUp(mainRL)
	}

	// Run CFRunLoop with DefaultMode
	// Note: kCFRunLoopCommonModes is NOT valid for CFRunLoopRunInMode - it's only
	// for adding sources/observers to multiple modes. Use kCFRunLoopDefaultMode.
	for i := 0; i < 5; i++ {
		result := CFRunLoopRunInMode(KCFRunLoopDefaultMode, 0.1, false)
		vlog("  CFRunLoop(DefaultMode) iteration %d: %s", i+1, runLoopResultString(result))
	}

	// Also run NSRunLoop to handle any Objective-C specific callbacks
	mainRunLoop := foundation.GetNSRunLoopClass().MainRunLoop()
	currentRunLoop := foundation.GetNSRunLoopClass().CurrentRunLoop()
	vlog("  NSRunLoop main: %#x, current: %#x, same=%v", mainRunLoop.ID, currentRunLoop.ID, mainRunLoop.ID == currentRunLoop.ID)

	futureDate := foundation.GetNSDateClass().DateWithTimeIntervalSinceNow(0.1)
	if futureDate.ID != 0 {
		if mainRunLoop.ID != 0 {
			ran := mainRunLoop.RunModeBeforeDate(foundation.RunLoopDefaultMode, &futureDate)
			vlog("  NSRunLoop.main.runMode: %v", ran)
		}
		if currentRunLoop.ID != 0 && currentRunLoop.ID != mainRunLoop.ID {
			futureDate2 := foundation.GetNSDateClass().DateWithTimeIntervalSinceNow(0.1)
			ran := currentRunLoop.RunModeBeforeDate(foundation.RunLoopDefaultMode, &futureDate2)
			vlog("  NSRunLoop.current.runMode: %v", ran)
		}
	}

	// Try using NSApplication to process events (handles XPC sources)
	sharedApp := getSharedApp()
	vlog("  NSApplication.sharedApplication: %#x", sharedApp.ID)
	if sharedApp.ID != 0 {
		// Process any pending events
		eventsProcessed := 0
		distantPast := foundation.GetNSDateClass().DistantPast()
		for i := 0; i < 5; i++ {
			// NSEventMaskAny = NSUIntegerMax
			// kCFRunLoopDefaultMode == "kCFRunLoopDefaultMode" as a string
			iEvent := sharedApp.NextEventMatchingMaskUntilDateInModeDequeue(
				appkit.NSEventMaskAny, distantPast, "kCFRunLoopDefaultMode", true)
			event := appkit.NSEventFromID(iEvent.GetID())
			if event.ID != 0 {
				eventsProcessed++
				vlog("  NSApp event %d: %#x", i, event.ID)
				sharedApp.SendEvent(&event)
			}
		}
		vlog("  NSApp events processed: %d", eventsProcessed)
	}

	vlog("=== runRunLoopAggressively END ===")
}

// Check if value is an NSError using generated binding

// getSharedApp returns the shared NSApplication instance.
// Defined locally to avoid circular dependency with console package.
func getSharedApp() appkit.NSApplication {
	nsAppID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSApplication")),
		objc.Sel("sharedApplication"),
	)
	return appkit.NSApplicationFromID(nsAppID)
}
