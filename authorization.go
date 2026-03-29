// authorization.go — Native macOS privilege escalation via AuthorizationServices.
//
// Shows the native macOS authentication dialog (Touch ID + password) via
// AuthorizationCopyRights, then executes the script as root.
//
// AuthorizationExecuteWithPrivileges is deprecated and causes SIGTRAP on
// macOS 26. We use AuthorizationCopyRights for the dialog + sudo for execution.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	securityLib     uintptr
	authCreate      func(rights uintptr, environment uintptr, flags uint32, authRef *uintptr) int32
	authCopyRights  func(authRef uintptr, rights uintptr, environment uintptr, flags uint32, authorizedRights *uintptr) int32
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
	purego.RegisterLibFunc(&authCopyRights, securityLib, "AuthorizationCopyRights")
	purego.RegisterLibFunc(&authFree, securityLib, "AuthorizationFree")
}

const (
	kAuthorizationFlagDefaults          uint32  = 0
	kAuthorizationFlagInteractionAllowed uint32 = 1 << 0
	kAuthorizationFlagExtendRights      uint32  = 1 << 1
	kAuthorizationFlagPreAuthorize      uint32  = 1 << 4
	kAuthorizationFlagDestroyRights     uint32  = 1 << 3
	kAuthorizationEmptyEnvironment      uintptr = 0
	errAuthorizationCanceled            int32   = -60006
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

// runElevatedBashNative shows the native macOS authorization dialog with
// Touch ID / password, then executes the script as root via sudo.
//
// AuthorizationCopyRights shows the dialog and caches credentials.
// AuthorizationExecuteWithPrivileges is NOT used (SIGTRAP on macOS 26).
// After authentication, we use sudo -n which succeeds with cached creds.
func runElevatedBashNative(scriptPath, prompt string) error {
	initAuthorizationServices()
	if authCreate == nil {
		return fmt.Errorf("authorization services not available")
	}

	var authRef uintptr
	status := authCreate(0, kAuthorizationEmptyEnvironment, kAuthorizationFlagDefaults, &authRef)
	if status != 0 {
		return fmt.Errorf("AuthorizationCreate: status %d", status)
	}
	defer authFree(authRef, kAuthorizationFlagDestroyRights)

	// Build rights for admin privileges.
	rightName := []byte("system.privilege.admin\x00")
	rightItem := authorizationItem{name: &rightName[0]}
	rights := authorizationItemSet{count: 1, items: &rightItem}

	// Build environment with prompt text.
	promptBytes := []byte(prompt)
	envKey := []byte("prompt\x00")
	promptItem := authorizationItem{
		name:        &envKey[0],
		valueLength: uint32(len(promptBytes)),
		value:       unsafe.Pointer(&promptBytes[0]),
	}
	env := authorizationItemSet{count: 1, items: &promptItem}

	// Show the native dialog (Touch ID / password).
	copyFlags := kAuthorizationFlagInteractionAllowed | kAuthorizationFlagExtendRights | kAuthorizationFlagPreAuthorize
	status = authCopyRights(
		authRef,
		uintptr(unsafe.Pointer(&rights)),
		uintptr(unsafe.Pointer(&env)),
		copyFlags,
		nil,
	)
	if status == errAuthorizationCanceled {
		return fmt.Errorf("interrupted: user cancelled authorization")
	}
	if status != 0 {
		return fmt.Errorf("authorization failed: status %d", status)
	}

	// User authenticated via native dialog. Execute the script as root.
	// sudo -n should work now because AuthorizationCopyRights cached the credential.
	cmd := exec.Command("sudo", "-n", "bash", scriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo after authorization: %w", err)
	}
	return nil
}
