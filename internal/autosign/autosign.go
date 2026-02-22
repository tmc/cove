// Package autosign checks for required entitlements at startup and
// ad-hoc signs the binary if they are missing, then re-execs.
// The entitlements plist is embedded so no source tree is needed.
package autosign

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

//go:embed vz.entitlements
var entitlements []byte

// EnsureEntitlements checks if the running binary has the virtualization
// entitlement. If not, it writes the embedded entitlements to a temp file,
// ad-hoc signs the binary, and re-execs. The re-exec is detected via an
// environment variable to prevent infinite loops.
func EnsureEntitlements() error {
	if os.Getenv("_VZ_SIGNED") == "1" {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve symlink: %w", err)
	}

	if hasentitlement(exe, "com.apple.security.virtualization") {
		return nil
	}

	// Write embedded entitlements to temp file for codesign
	tmp, err := os.CreateTemp("", "vz-entitlements-*.plist")
	if err != nil {
		return fmt.Errorf("create temp entitlements: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(entitlements); err != nil {
		return fmt.Errorf("write entitlements: %w", err)
	}
	tmp.Close()

	cmd := exec.Command("codesign", "-s", "-", "-f", "--entitlements", tmp.Name(), exe)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codesign failed: %s: %w", stderr.String(), err)
	}

	// Re-exec with marker to prevent loop
	env := append(os.Environ(), "_VZ_SIGNED=1")
	return syscall.Exec(exe, os.Args, env)
}

func hasentitlement(exe, key string) bool {
	cmd := exec.Command("codesign", "-d", "--entitlements", "-", exe)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return bytes.Contains(out, []byte(key))
}
