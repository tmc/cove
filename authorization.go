// authorization.go — Native macOS privilege escalation via AuthorizationServices.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Authorization types matching Security framework definitions.
type authorizationRef uintptr

// Authorization flags.
const (
	kAuthorizationFlagDefaults            = 0
	kAuthorizationFlagInteractionAllowed  = 1 << 0
	kAuthorizationFlagExtendRights        = 1 << 1
	kAuthorizationFlagPartialRights       = 1 << 2
	kAuthorizationFlagDestroyRights       = 1 << 3
	kAuthorizationFlagPreAuthorize        = 1 << 4
	errAuthorizationSuccess               = 0
	errAuthorizationCanceled              = -60006
	errAuthorizationInteractionNotAllowed = -60005
	errAuthorizationToolExecuteFailure    = -60031
)

var (
	authOnce    sync.Once
	authInitErr error
	// AuthorizationCreate(rights, env, flags, *auth) -> OSStatus
	fnAuthorizationCreate func(rights, env uintptr, flags uint32, auth *authorizationRef) int32
	// AuthorizationExecuteWithPrivileges(auth, tool, opts, args, pipe) -> OSStatus
	fnAuthorizationExecWithPrivs func(auth authorizationRef, tool uintptr, opts uint32, args uintptr, pipe uintptr) int32
	// AuthorizationFree(auth, flags) -> OSStatus
	fnAuthorizationFree func(auth authorizationRef, flags uint32) int32
)

func initAuthorization() {
	authOnce.Do(func() {
		sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_LAZY)
		if err != nil {
			authInitErr = fmt.Errorf("dlopen Security: %w", err)
			return
		}
		purego.RegisterLibFunc(&fnAuthorizationCreate, sec, "AuthorizationCreate")
		purego.RegisterLibFunc(&fnAuthorizationExecWithPrivs, sec, "AuthorizationExecuteWithPrivileges")
		purego.RegisterLibFunc(&fnAuthorizationFree, sec, "AuthorizationFree")
	})
}

// runElevatedBashNative runs a bash script with root privileges using the
// native macOS authorization dialog (Touch ID / password). Shows the standard
// system authentication prompt.
func runElevatedBashNative(scriptPath string) error {
	initAuthorization()
	if authInitErr != nil {
		return fmt.Errorf("authorization init: %w", authInitErr)
	}

	// Create an authorization reference with interactive rights.
	var auth authorizationRef
	flags := uint32(kAuthorizationFlagDefaults |
		kAuthorizationFlagInteractionAllowed |
		kAuthorizationFlagExtendRights |
		kAuthorizationFlagPreAuthorize)

	status := fnAuthorizationCreate(0, 0, flags, &auth)
	if status != errAuthorizationSuccess {
		return authStatusError("AuthorizationCreate", status)
	}
	defer fnAuthorizationFree(auth, uint32(kAuthorizationFlagDestroyRights))

	// Build null-terminated C strings and argument array.
	tool := appendNull([]byte("/bin/bash"))
	arg := appendNull([]byte(scriptPath))

	// Arguments: [scriptPath, NULL]
	// The tool (/bin/bash) is separate; args start after it.
	argv := [2]uintptr{
		uintptr(unsafe.Pointer(&arg[0])),
		0, // NULL terminator
	}

	var pipe uintptr
	status = fnAuthorizationExecWithPrivs(
		auth,
		uintptr(unsafe.Pointer(&tool[0])),
		0, // options (must be 0)
		uintptr(unsafe.Pointer(&argv[0])),
		uintptr(unsafe.Pointer(&pipe)),
	)

	// Keep Go-allocated memory alive until C call returns.
	runtime.KeepAlive(tool)
	runtime.KeepAlive(arg)
	runtime.KeepAlive(argv)

	if status != errAuthorizationSuccess {
		return authStatusError("AuthorizationExecuteWithPrivileges", status)
	}

	// Drain output from the communications pipe.
	if pipe != 0 {
		drainAuthPipe(pipe)
	}

	return nil
}

// drainAuthPipe reads all output from the FILE* returned by
// AuthorizationExecuteWithPrivileges.
func drainAuthPipe(filep uintptr) {
	fd := cFileNo(filep)
	if fd < 0 {
		cFClose(filep)
		return
	}
	f := os.NewFile(uintptr(fd), "auth-pipe")
	if f == nil {
		cFClose(filep)
		return
	}
	r := bufio.NewReader(f)
	_, _ = io.Copy(os.Stdout, r)
	// fclose closes the underlying fd too.
	cFClose(filep)
}

func authStatusError(fn string, status int32) error {
	switch status {
	case errAuthorizationCanceled:
		return fmt.Errorf("interrupted: user cancelled authorization")
	case errAuthorizationInteractionNotAllowed:
		return fmt.Errorf("%s: interaction not allowed (headless?)", fn)
	case errAuthorizationToolExecuteFailure:
		return fmt.Errorf("%s: tool execution failed", fn)
	default:
		return fmt.Errorf("%s: OSStatus %d", fn, status)
	}
}

// appendNull returns s with a trailing NUL byte for C interop.
func appendNull(s []byte) []byte {
	return append(s, 0)
}

var (
	libcOnce sync.Once
	fnFileno func(stream uintptr) int32
	fnFclose func(stream uintptr) int32
)

func initLibC() {
	libcOnce.Do(func() {
		libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&fnFileno, libc, "fileno")
		purego.RegisterLibFunc(&fnFclose, libc, "fclose")
	})
}

func cFileNo(fp uintptr) int32 {
	initLibC()
	if fnFileno == nil {
		return -1
	}
	return fnFileno(fp)
}

func cFClose(fp uintptr) {
	initLibC()
	if fnFclose != nil {
		fnFclose(fp)
	}
}
