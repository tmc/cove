// authorization.go — Native macOS privilege escalation via AuthorizationServices.
//
// Shows the native macOS authentication dialog (Touch ID + password) with
// "vz-macos" as the requesting application and a custom prompt. After the
// user authenticates, executes the script as root via
// AuthorizationExecuteWithPrivileges or sudo.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	securityLib     uintptr
	authCreate      func(rights uintptr, environment uintptr, flags uint32, authRef *uintptr) int32
	authCopyRights  func(authRef uintptr, rights uintptr, environment uintptr, flags uint32, authorizedRights *uintptr) int32
	authFree        func(authRef uintptr, flags uint32) int32
	authExecPriv    func(authRef uintptr, tool *byte, args **byte, file uintptr) int32
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
	purego.RegisterLibFunc(&authExecPriv, securityLib, "AuthorizationExecuteWithPrivileges")
}

const (
	kAuthorizationFlagDefaults           uint32  = 0
	kAuthorizationFlagInteractionAllowed uint32  = 1 << 0
	kAuthorizationFlagExtendRights       uint32  = 1 << 1
	kAuthorizationFlagPreAuthorize       uint32  = 1 << 4
	kAuthorizationFlagDestroyRights      uint32  = 1 << 3
	kAuthorizationEmptyEnvironment       uintptr = 0
	errAuthorizationCanceled             int32   = -60006
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
// "vz-macos" as the requesting application and the given prompt, then
// executes the script as root.
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

	// User authenticated via native "vz-macos" dialog.
	// Try to execute the script as root.

	// Method 1: AuthorizationExecuteWithPrivileges with root verification.
	if authExecPriv != nil {
		if err := authExecVerified(authRef, scriptPath); err == nil {
			return nil
		}
	}

	// Method 2: sudo -n (non-interactive, cached credentials).
	cmd := exec.Command("sudo", "-n", "bash", scriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.Run() == nil {
		return nil
	}

	// Both methods failed. Return an error suggesting sudo.
	return fmt.Errorf("authorization succeeded but could not execute as root; re-run with: sudo bash %s", scriptPath)
}

// authExecVerified runs the script via AuthorizationExecuteWithPrivileges
// and verifies that it actually executed as root by checking a marker file.
// On macOS 26+ the API may return success without elevating.
func authExecVerified(authRef uintptr, scriptPath string) error {
	// Create a temporary marker path.
	marker, err := os.CreateTemp("", "vz-root-check-*")
	if err != nil {
		return err
	}
	markerPath := marker.Name()
	marker.Close()
	os.Remove(markerPath)
	defer os.Remove(markerPath)

	// Create a wrapper that checks root, writes the marker, then runs the real script.
	wrapper, err := os.CreateTemp("", "vz-root-wrap-*.sh")
	if err != nil {
		return err
	}
	wrapperPath := wrapper.Name()
	defer os.Remove(wrapperPath)

	fmt.Fprintf(wrapper, `#!/bin/bash
if [ "$(id -u)" -ne 0 ]; then
  exit 77
fi
touch %q
exec bash %q
`, markerPath, scriptPath)
	wrapper.Close()
	os.Chmod(wrapperPath, 0700)

	// Execute via AuthorizationExecuteWithPrivileges.
	toolPath := []byte("/bin/bash\x00")
	arg := append([]byte(wrapperPath), 0)
	args := []*byte{&arg[0], nil}

	status := authExecPriv(authRef, &toolPath[0], &args[0], 0)
	if status != 0 {
		return fmt.Errorf("status %d", status)
	}

	// Wait for the marker to appear (up to 5 seconds).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(markerPath); err == nil {
			// Confirmed running as root. Wait for completion.
			waitForCompletion(wrapperPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("did not execute as root")
}

// waitForCompletion waits for the given script's bash process to exit.
func waitForCompletion(wrapperPath string) {
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		// Check if any bash process is still running this wrapper.
		out, err := exec.Command("pgrep", "-f", wrapperPath).Output()
		if err != nil || len(out) == 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
