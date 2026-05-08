//go:build darwin

package sckit

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/objc"
)

const sckitMinMacOSMajor = 14

var (
	cgPreflightOnce               sync.Once
	cgPreflightScreenCaptureAccess func() bool
)

func loadCGPreflight() {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
