// Package diskimages2 provides convenience wrappers around the generated
// DiskImages2 framework bindings. It offers a high-level Go API for disk image
// operations (attach, detach, create, resize, info) while delegating the
// low-level ObjC interop to the generated package.
package diskimages2

import (
	"errors"

	"github.com/tmc/appledocs/generated/objc"

	// Import generated package to trigger framework loading via its init().
	di2 "github.com/tmc/appledocs/generated/diskimages2"
)

// ErrFrameworkUnavailable is returned when DiskImages2.framework cannot be loaded.
var ErrFrameworkUnavailable = errors.New("diskimages2: framework not available")

// Available reports whether DiskImages2.framework is loaded and usable.
func Available() bool {
	// The generated package loads the framework in init().
	// Check if the main facade class is resolvable.
	return objc.GetClass("DiskImages2") != 0
}

func ensureLoaded() error {
	if !Available() {
		return ErrFrameworkUnavailable
	}
	return nil
}

// Keep a reference so the import isn't unused.
var _ = di2.GetDIAttachParamsClass
