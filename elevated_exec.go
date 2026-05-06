// elevated_exec.go — Pure-Go re-exec helper for AuthorizationExecuteWithPrivileges.
//
// AEWP launches the named tool with permission to call setuid(0); it does NOT
// itself run the tool as root. Rather than launching /bin/bash (which would
// give an attacker who can write the script file root via TOCTOU), we launch
// cove itself with a hidden subcommand that:
//
//  1. Calls syscall.Setuid(0) so the process is actually root.
//  2. Reads the manifest path + expected SHA-256 from argv.
//  3. Verifies the manifest content matches the hash the parent computed
//     before calling AEWP — this closes the TOCTOU window where an attacker
//     could swap manifest contents between staging and execution.
//  4. Dispatches to a small set of typed file operations (copy, chown, mkdir,
//     mount remount, launchctl bootstrap/bootout, rm). No bash, no shell,
//     no arbitrary-script execution path.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const elevatedOpArg = "__elevated-op"

// elevatedManifest describes a fixed set of privileged file/launchctl
// operations. Anything not expressible here cannot be done via the elevation
// path — by design.
type elevatedManifest struct {
	// RemountOwners runs `diskutil enableOwnership` then `mount -uo owners`
	// on each partition, and verifies the resulting mount no longer has the
	// noowners flag. Used to make APFS chowns actually stick on disk-image
	// volumes.
	RemountOwners []string `json:"remountOwners,omitempty"`

	// MkdirAll creates each path with mode 0755 (root:wheel).
	MkdirAll []string `json:"mkdirAll,omitempty"`

	// CopyFiles copies Src→Dst, then sets Mode and Owner.
	CopyFiles []elevatedCopy `json:"copyFiles,omitempty"`

	// ChownFiles applies Owner to each existing path.
	ChownFiles []elevatedChown `json:"chownFiles,omitempty"`

	// RemoveFiles deletes each path (no error if missing).
	RemoveFiles []string `json:"removeFiles,omitempty"`

	// LaunchctlBootout boots out each label (no error if not loaded).
	LaunchctlBootout []string `json:"launchctlBootout,omitempty"`

	// LaunchctlBootstrap loads each plist into the system domain.
	LaunchctlBootstrap []string `json:"launchctlBootstrap,omitempty"`

	// VerifyChownTarget, if non-empty, checks that the path's stat
	// reports owner uid=0 gid=0; failure aborts with a clear message.
	VerifyChownTarget string `json:"verifyChownTarget,omitempty"`

	// VerifyChownTargets checks every listed path for root:wheel ownership.
	VerifyChownTargets []string `json:"verifyChownTargets,omitempty"`

	// SuccessMarker, if non-empty, is a path that gets touched at the end
	// of a successful run. The (unprivileged) caller can stat it to
	// distinguish "child crashed" from "child completed".
	SuccessMarker string `json:"successMarker,omitempty"`
}

type elevatedCopy struct {
	Src   string `json:"src"`
	Dst   string `json:"dst"`
	Mode  string `json:"mode,omitempty"`  // octal, e.g. "0755"
	Owner string `json:"owner,omitempty"` // "root:wheel" or "" to leave alone
}

type elevatedChown struct {
	Path  string `json:"path"`
	Owner string `json:"owner"` // "root:wheel"
}

// maybeRunElevatedOp checks os.Args for the hidden re-exec subcommand and,
// if present, becomes root and runs the typed manifest. Never returns on
// the success path. Safe to call before flag.Parse.
//
// argv: cove __elevated-op <manifestPath> <expectedSHA256>
func maybeRunElevatedOp() {
	if len(os.Args) < 4 || os.Args[1] != elevatedOpArg {
		return
	}
	manifestPath := os.Args[2]
	expectedHash := os.Args[3]

	if err := syscall.Setuid(0); err != nil {
		fmt.Fprintf(os.Stderr, "elevated-op: setuid(0): %v\n", err)
		os.Exit(1)
	}

	// TOCTOU defense: re-read the manifest after becoming root and verify
	// it still matches the hash the unprivileged parent computed before
	// calling AEWP. If an attacker swapped the file, the hashes diverge.
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "elevated-op: read manifest: %v\n", err)
		os.Exit(1)
	}
	gotHash := sha256.Sum256(body)
	if hex.EncodeToString(gotHash[:]) != expectedHash {
		fmt.Fprintf(os.Stderr, "elevated-op: manifest hash mismatch (file tampered between staging and exec)\n")
		os.Exit(1)
	}

	var m elevatedManifest
	if err := json.Unmarshal(body, &m); err != nil {
		fmt.Fprintf(os.Stderr, "elevated-op: parse manifest: %v\n", err)
		os.Exit(1)
	}

	if err := runElevatedManifest(&m); err != nil {
		fmt.Fprintf(os.Stderr, "elevated-op: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// runElevatedManifest executes each section of the manifest in order. Any
// failure aborts the remaining steps. Runs as the calling process (which
// must already be root).
func runElevatedManifest(m *elevatedManifest) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("not running as root (euid=%d)", os.Geteuid())
	}

	for _, part := range m.RemountOwners {
		if err := remountWithOwners(part); err != nil {
			return fmt.Errorf("remount %s: %w", part, err)
		}
	}

	for _, dir := range m.MkdirAll {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		if err := os.Chown(dir, 0, 0); err != nil {
			return fmt.Errorf("chown mkdir %s: %w", dir, err)
		}
	}

	for _, c := range m.CopyFiles {
		if err := copyFileWithModeOwner(c.Src, c.Dst, c.Mode, c.Owner); err != nil {
			return fmt.Errorf("copy %s -> %s: %w", c.Src, c.Dst, err)
		}
	}

	for _, c := range m.ChownFiles {
		uid, gid, err := parseOwner(c.Owner)
		if err != nil {
			return fmt.Errorf("chown %s: %w", c.Path, err)
		}
		if err := os.Chown(c.Path, uid, gid); err != nil {
			return fmt.Errorf("chown %s: %w", c.Path, err)
		}
	}

	for _, p := range m.RemoveFiles {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}

	for _, label := range m.LaunchctlBootout {
		// "no-op if not loaded" — ignore exit code.
		_ = exec.Command("launchctl", "bootout", "system/"+label).Run()
	}

	for _, plistPath := range m.LaunchctlBootstrap {
		out, err := exec.Command("launchctl", "bootstrap", "system", plistPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("launchctl bootstrap %s: %w (%s)", plistPath, err, strings.TrimSpace(string(out)))
		}
	}

	verifyTargets := m.VerifyChownTargets
	if m.VerifyChownTarget != "" {
		verifyTargets = append(verifyTargets, m.VerifyChownTarget)
	}
	for _, target := range verifyTargets {
		if err := verifyRootWheel(target); err != nil {
			return err
		}
	}

	if m.SuccessMarker != "" {
		f, err := os.Create(m.SuccessMarker)
		if err != nil {
			return fmt.Errorf("success marker: %w", err)
		}
		f.Close()
	}
	return nil
}

func verifyRootWheel(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("verify-chown stat %s: %w", path, err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("verify-chown: unexpected stat type for %s", path)
	}
	if sys.Uid != 0 || sys.Gid != 0 {
		return fmt.Errorf("chown root:wheel did not stick on %s (owner=%d:%d); launchd will reject daemon plists",
			path, sys.Uid, sys.Gid)
	}
	return nil
}

func remountWithOwners(partition string) error {
	if out, err := exec.Command("diskutil", "enableOwnership", partition).CombinedOutput(); err != nil {
		return fmt.Errorf("enableOwnership: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("mount", "-uo", "owners", partition).CombinedOutput(); err != nil {
		return fmt.Errorf("mount -uo owners: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	out, err := exec.Command("mount").Output()
	if err != nil {
		return fmt.Errorf("mount: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, partition) {
			continue
		}
		if strings.Contains(line, "noowners") {
			return fmt.Errorf("%s still mounted with noowners after 'mount -uo owners' — chown would silently no-op", partition)
		}
	}
	return nil
}

func copyFileWithModeOwner(src, dst, mode, owner string) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read src: %w", err)
	}
	if err := os.WriteFile(dst, body, 0600); err != nil {
		return fmt.Errorf("write dst: %w", err)
	}
	if mode != "" {
		m, err := strconv.ParseUint(mode, 8, 32)
		if err != nil {
			return fmt.Errorf("parse mode %q: %w", mode, err)
		}
		if err := os.Chmod(dst, os.FileMode(m)); err != nil {
			return fmt.Errorf("chmod: %w", err)
		}
	}
	if owner != "" {
		uid, gid, err := parseOwner(owner)
		if err != nil {
			return err
		}
		if err := os.Chown(dst, uid, gid); err != nil {
			return fmt.Errorf("chown: %w", err)
		}
	}
	return nil
}

func parseOwner(owner string) (uid, gid int, err error) {
	switch owner {
	case "root:wheel":
		return 0, 0, nil
	default:
		return 0, 0, fmt.Errorf("unsupported owner %q (only root:wheel allowed)", owner)
	}
}
