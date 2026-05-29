//go:build darwin

package main

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	appSandboxEntitlementOnce sync.Once
	appSandboxEntitlementOK   bool

	secTaskCreateFromSelf          func(uintptr) uintptr
	secTaskCopyValueForEntitlement func(uintptr, uintptr, uintptr) uintptr
	appSandboxCFStringCreate       func(uintptr, *byte, uint32) uintptr
	appSandboxCFRelease            func(uintptr)
	appSandboxCFBooleanTrue        uintptr
)

func appleAppSandboxEntitlement() bool {
	appSandboxEntitlementOnce.Do(func() {
		sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_LAZY)
		if err != nil {
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&secTaskCreateFromSelf, sec, "SecTaskCreateFromSelf")
		purego.RegisterLibFunc(&secTaskCopyValueForEntitlement, sec, "SecTaskCopyValueForEntitlement")
		purego.RegisterLibFunc(&appSandboxCFStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&appSandboxCFRelease, cf, "CFRelease")
		appSandboxCFBooleanTrue = loadPtrGlobal(cf, "kCFBooleanTrue")
		if appSandboxCFBooleanTrue == 0 {
			return
		}
		task := secTaskCreateFromSelf(0)
		if task == 0 {
			return
		}
		defer appSandboxCFRelease(task)
		keyBytes := append([]byte("com.apple.security.app-sandbox"), 0)
		key := appSandboxCFStringCreate(0, (*byte)(unsafe.Pointer(&keyBytes[0])), kCFStringEncodingUTF8)
		if key == 0 {
			return
		}
		defer appSandboxCFRelease(key)
		value := secTaskCopyValueForEntitlement(task, key, 0)
		if value == 0 {
			return
		}
		defer appSandboxCFRelease(value)
		appSandboxEntitlementOK = value == appSandboxCFBooleanTrue
	})
	return appSandboxEntitlementOK
}
