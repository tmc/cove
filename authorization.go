// authorization.go — Native macOS privilege escalation via AuthorizationServices.
//
// This uses the Security framework to obtain an authorization reference and
// then launches a privileged tool with AuthorizationExecuteWithPrivileges.
// It avoids collecting the raw host admin password in app code.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/term"
)

var (
	securityLib     uintptr
	authCreate      func(rights uintptr, environment uintptr, flags uint32, authRef *uintptr) int32
	authExecute     func(authRef uintptr, pathToTool uintptr, options uint32, arguments uintptr, communicationsPipe *uintptr) int32
	authFree        func(authRef uintptr, flags uint32) int32
	authInitialized bool

	libcFread  func(buf uintptr, size uintptr, n uintptr, fp uintptr) uintptr
	libcFclose func(fp uintptr) int32
)

var (
	authCreateNoUITimeout      = 90 * time.Second
	authCreatePromptTimeout    = 15 * time.Minute
	authCreatePollInterval     = 500 * time.Millisecond
	authorizationPromptVisible = defaultAuthorizationPromptVisible
	authorizationStdinIsTTY    = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
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

	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY)
	if err == nil {
		purego.RegisterLibFunc(&libcFread, libc, "fread")
		purego.RegisterLibFunc(&libcFclose, libc, "fclose")
	}
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

// PreWarm obtains the admin authorization right early so later provisioning
// work can reuse the OS credential cache.
func PreWarm() error {
	return preWarmAuthorization(elevationPrompt("Prepare to provision the VM disk."))
}

func preWarmAuthorization(prompt string) error {
	return runOffUIThread(func() error {
		authRef, err := createAuthorization(prompt)
		if err != nil {
			return err
		}
		authFree(authRef, kAuthorizationFlagDestroyRights)
		return nil
	})
}

// runElevatedManifestNative shows the native macOS authorization dialog and
// then re-execs cove with __elevated-op, which becomes root via setuid(0) and
// runs the typed manifest. The manifest content is hashed by the parent;
// the elevated child verifies the file still matches that hash before acting,
// closing the TOCTOU window where an attacker could swap manifest contents
// between staging and execution.
//
// The prompt argument is shown to the user inside the dialog body, after the
// SecurityAgent's own "<app> wants to make changes" line. Keep it short and
// action-oriented (one sentence). Name the affected VM when relevant so the
// user can tell what they are approving.
func runElevatedManifestNative(manifestPath, sha256Hex, prompt string) error {
	if !authorizationStdinIsTTY() {
		return fmt.Errorf("native authorization requires an interactive terminal; re-run the provisioning command with sudo")
	}
	if onUIThread() {
		return fmt.Errorf("native authorization cannot run on the app ui thread; run provisioning from a worker goroutine")
	}
	authRef, err := createAuthorization(prompt)
	if err != nil {
		return err
	}
	defer authFree(authRef, kAuthorizationFlagDestroyRights)

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cove binary for elevated exec: %w", err)
	}
	toolPath, err := syscall.BytePtrFromString(exePath)
	if err != nil {
		return fmt.Errorf("authorization tool path: %w", err)
	}
	subcmdArg, err := syscall.BytePtrFromString(elevatedOpArg)
	if err != nil {
		return fmt.Errorf("authorization subcmd arg: %w", err)
	}
	manifestArg, err := syscall.BytePtrFromString(manifestPath)
	if err != nil {
		return fmt.Errorf("authorization manifest path: %w", err)
	}
	hashArg, err := syscall.BytePtrFromString(sha256Hex)
	if err != nil {
		return fmt.Errorf("authorization manifest hash: %w", err)
	}

	argv := []*byte{subcmdArg, manifestArg, hashArg, nil}
	return executeAuthorizedTool(authRef, uintptr(unsafe.Pointer(toolPath)), uintptr(unsafe.Pointer(&argv[0])))
}

func executeAuthorizedTool(authRef, toolPath, argv uintptr) error {
	type executeResult struct {
		status int32
	}
	done := make(chan executeResult, 1)
	go func() {
		var commPipe uintptr
		status := authExecute(authRef, toolPath, 0, argv, &commPipe)
		if commPipe != 0 && libcFread != nil {
			var buf [4096]byte
			for {
				n := libcFread(uintptr(unsafe.Pointer(&buf[0])), 1, uintptr(len(buf)), commPipe)
				if n == 0 {
					break
				}
				os.Stdout.Write(buf[:n])
			}
			if libcFclose != nil {
				libcFclose(commPipe)
			}
		}
		done <- executeResult{status: status}
	}()

	ticker := time.NewTicker(authCreatePollInterval)
	defer ticker.Stop()
	start := time.Now()
	promptSeenAt := time.Time{}
	waitingForApprovalLogged := false
	for {
		select {
		case result := <-done:
			if result.status == errAuthorizationCanceled {
				return fmt.Errorf("interrupted: user cancelled authorization")
			}
			if result.status != 0 {
				return fmt.Errorf("AuthorizationExecuteWithPrivileges: status %d", result.status)
			}
			return nil
		case <-ticker.C:
			if authorizationPromptVisible() {
				if promptSeenAt.IsZero() {
					promptSeenAt = time.Now()
				}
				if !waitingForApprovalLogged {
					fmt.Fprintln(os.Stderr, "Waiting for macOS admin-password dialog approval...")
					waitingForApprovalLogged = true
				}
				if time.Since(promptSeenAt) > authCreatePromptTimeout {
					return fmt.Errorf("authorization execute dialog still pending after %s; approve or cancel the macOS prompt and retry", authCreatePromptTimeout)
				}
				continue
			}
			if time.Since(start) > authCreateNoUITimeout {
				return fmt.Errorf("AuthorizationExecuteWithPrivileges wedged after %s (likely authd/SecurityAgent unresponsive); try logging out and back in, or run the script manually as root", authCreateNoUITimeout)
			}
		}
	}
}

func createAuthorization(prompt string) (uintptr, error) {
	initAuthorizationServices()
	if authCreate == nil || authExecute == nil {
		return 0, fmt.Errorf("authorization services not available")
	}

	exePath, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("locate cove binary for elevated exec: %w", err)
	}
	toolPath, err := syscall.BytePtrFromString(exePath)
	if err != nil {
		return 0, fmt.Errorf("authorization tool path: %w", err)
	}

	// authorizationItem.name is a C string (NUL-terminated): Security.framework
	// calls strlen() on it. A bare []byte conversion is NOT NUL-terminated and
	// causes SIGBUS when strlen walks past the buffer into unmapped pages.
	rightName, err := syscall.BytePtrFromString(kAuthorizationRightExecute)
	if err != nil {
		return 0, fmt.Errorf("authorization right name: %w", err)
	}
	envKey, err := syscall.BytePtrFromString(kAuthorizationEnvironmentPrompt)
	if err != nil {
		return 0, fmt.Errorf("authorization env key: %w", err)
	}

	// Build rights for the privileged tool itself.
	rightItem := authorizationItem{
		name:        rightName,
		valueLength: uint32(len(exePath)),
		value:       unsafe.Pointer(toolPath),
	}
	rights := authorizationItemSet{count: 1, items: &rightItem}

	// Build environment with prompt text. The prompt value is a UTF-8 string
	// whose length excludes any terminator, per Security.framework docs.
	// Use BytePtrFromString to guarantee NUL-termination — Security.framework
	// has been observed calling strlen() on the value, which SIGBUSes when a
	// bare []byte runs up against an unmapped page.
	promptPtr, err := syscall.BytePtrFromString(prompt)
	if err != nil {
		return 0, fmt.Errorf("authorization prompt: %w", err)
	}
	promptItem := authorizationItem{
		name:        envKey,
		valueLength: uint32(len(prompt)),
		value:       unsafe.Pointer(promptPtr),
	}
	env := authorizationItemSet{count: 1, items: &promptItem}

	// AuthorizationCreate can wedge inside its XPC call to authd in some
	// states (observed: setItemSet → xpc_dictionary_set_data hanging
	// indefinitely with no SecurityAgent ever spawning). Watchdog the call
	// so a stuck authd does not freeze cove forever — surface a clear error
	// instead and let the caller fall back to a different elevation path.
	var authRef uintptr
	var status int32
	done := make(chan struct{})
	go func() {
		status = authCreate(
			uintptr(unsafe.Pointer(&rights)),
			uintptr(unsafe.Pointer(&env)),
			kAuthorizationFlagInteractionAllowed|kAuthorizationFlagExtendRights|kAuthorizationFlagPreAuthorize,
			&authRef,
		)
		close(done)
	}()
	ticker := time.NewTicker(authCreatePollInterval)
	defer ticker.Stop()
	start := time.Now()
	promptSeenAt := time.Time{}
	waitingForApprovalLogged := false
	for {
		select {
		case <-done:
			goto authCreateDone
		case <-ticker.C:
			if authorizationPromptVisible() {
				if promptSeenAt.IsZero() {
					promptSeenAt = time.Now()
				}
				if !waitingForApprovalLogged {
					fmt.Fprintln(os.Stderr, "Waiting for macOS admin-password dialog approval...")
					waitingForApprovalLogged = true
				}
				if time.Since(promptSeenAt) > authCreatePromptTimeout {
					return 0, fmt.Errorf("authorization dialog still pending after %s; approve or cancel the macOS prompt and retry", authCreatePromptTimeout)
				}
				continue
			}
			if time.Since(start) > authCreateNoUITimeout {
				return 0, fmt.Errorf("AuthorizationCreate wedged after %s (likely authd/SecurityAgent unresponsive); try logging out and back in, or run the script manually as root", authCreateNoUITimeout)
			}
		}
	}
authCreateDone:
	if status == errAuthorizationCanceled {
		return 0, fmt.Errorf("interrupted: user cancelled authorization")
	}
	if status != 0 {
		return 0, fmt.Errorf("AuthorizationCreate: status %d", status)
	}
	return authRef, nil
}

func defaultAuthorizationPromptVisible() bool {
	for _, name := range []string{"SecurityAgent", "authorizationhost"} {
		if err := exec.Command("pgrep", "-x", name).Run(); err == nil {
			return true
		}
	}
	return false
}
