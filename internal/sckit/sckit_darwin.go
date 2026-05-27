//go:build darwin

package sckit

import (
	"strings"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/objc"
	"golang.org/x/sys/unix"
)

const sckitMinMacOSMajor = 14

var (
	cgPreflightOnce                sync.Once
	cgPreflightScreenCaptureAccess func() bool
	coreGraphicsFrameworkPath      = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
)

func loadCGPreflight() {
	cg, err := purego.Dlopen(coreGraphicsFrameworkPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	purego.RegisterLibFunc(&cgPreflightScreenCaptureAccess, cg, "CGPreflightScreenCaptureAccess")
}

// Detect inspects the host for ScreenCaptureKit readiness without
// prompting the user. It is safe to call from any goroutine.
func Detect() Probe {
	p := Probe{MacOSVersion: readMacOSVersion()}

	cgPreflightOnce.Do(loadCGPreflight)
	if cgPreflightScreenCaptureAccess != nil {
		p.ScreenRecordingAuthorized = cgPreflightScreenCaptureAccess()
	}

	scStream := objc.GetClass("SCStream")
	classOK := scStream != 0
	versionOK := macOSVersionAtLeast(p.MacOSVersion, sckitMinMacOSMajor, 0)
	p.SCKitAvailable = classOK && versionOK

	return p
}

func readMacOSVersion() string {
	version, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(version)
}
