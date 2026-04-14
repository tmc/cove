// authorization.go — Native macOS privilege escalation via AuthorizationServices.
//
// This uses the Security framework to obtain an authorization reference and
// then launches a privileged tool with AuthorizationExecuteWithPrivileges.
// It avoids collecting the raw host admin password in app code.
package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	securityLib     uintptr
	authCreate      func(rights uintptr, environment uintptr, flags uint32, authRef *uintptr) int32
	authExecute     func(authRef uintptr, pathToTool uintptr, options uint32, arguments uintptr, communicationsPipe uintptr) int32
	authFree        func(authRef uintptr, flags uint32) int32
	authInitialized bool
)

func initAuthorizationServices() {
	if authInitialized {
		return
	}
	authInitialized = true

	lib, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_LAZY)
	if err != nil {
		return
	}
	securityLib = lib

	purego.RegisterLibFunc(&authCreate, securityLib, "AuthorizationCreate")
	purego.RegisterLibFunc(&authExecute, securityLib, "AuthorizationExecuteWithPrivileges")
	purego.RegisterLibFunc(&authFree, securityLib, "AuthorizationFree")
}

const (
	kAuthorizationFlagDefaults           uint32  = 0
	kAuthorizationFlagInteractionAllowed uint32  = 1 << 0
	kAuthorizationFlagExtendRights       uint32  = 1 << 1
	kAuthorizationFlagPreAuthorize       uint32  = 1 << 4
	kAuthorizationFlagDestroyRights      uint32  = 1 << 3
	kAuthorizationEmptyEnvironment       uintptr = 0
	errAuthorizationCanceled             int32   = -60006
	kAuthorizationRightExecute                   = "system.privilege.admin"
	kAuthorizationEnvironmentPrompt              = "prompt"
)

type authorizationItem struct {
	name        *byte
	valueLength uint32
	value       unsafe.Pointer
	flags       uint32
}

type authorizationItemSet struct {
	count uint32
	items *authorizationItem
}

// runElevatedBashNative shows the native macOS authorization dialog and then
// executes the script as root via AuthorizationExecuteWithPrivileges.
func runElevatedBashNative(scriptPath, prompt string) error {
	initAuthorizationServices()
	if authCreate == nil || authExecute == nil {
		return fmt.Errorf("authorization services not available")
	}

	toolPath, err := syscall.BytePtrFromString("/bin/bash")
	if err != nil {
		return fmt.Errorf("authorization tool path: %w", err)
	}
	scriptArg, err := syscall.BytePtrFromString(scriptPath)
	if err != nil {
		return fmt.Errorf("authorization script path: %w", err)
	}

	// Build rights for the privileged tool itself.
	rightName := []byte(kAuthorizationRightExecute)
	rightItem := authorizationItem{
		name:        &rightName[0],
		valueLength: uint32(len("/bin/bash")),
		value:       unsafe.Pointer(toolPath),
	}
	rights := authorizationItemSet{count: 1, items: &rightItem}

	// Build environment with prompt text.
	promptBytes := []byte(prompt)
	envKey := []byte(kAuthorizationEnvironmentPrompt)
	promptItem := authorizationItem{
		name:        &envKey[0],
		valueLength: uint32(len(promptBytes)),
		value:       unsafe.Pointer(&promptBytes[0]),
	}
	env := authorizationItemSet{count: 1, items: &promptItem}

	var authRef uintptr
	status := authCreate(
		uintptr(unsafe.Pointer(&rights)),
		uintptr(unsafe.Pointer(&env)),
		kAuthorizationFlagInteractionAllowed|kAuthorizationFlagExtendRights|kAuthorizationFlagPreAuthorize,
		&authRef,
	)
	if status == errAuthorizationCanceled {
		return fmt.Errorf("interrupted: user cancelled authorization")
	}
	if status != 0 {
		return fmt.Errorf("AuthorizationCreate: status %d", status)
	}
	defer authFree(authRef, kAuthorizationFlagDestroyRights)

	argv := []*byte{scriptArg, nil}
	status = authExecute(
		authRef,
		uintptr(unsafe.Pointer(toolPath)),
		0,
		uintptr(unsafe.Pointer(&argv[0])),
		0,
	)
	if status == errAuthorizationCanceled {
		return fmt.Errorf("interrupted: user cancelled authorization")
	}
	if status != 0 {
		return fmt.Errorf("AuthorizationExecuteWithPrivileges: status %d", status)
	}
	return nil
}
