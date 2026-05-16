// elevated_run.go — High-level wrapper around the typed elevation path.
//
// runElevated is the single entrypoint used by all callers that need root
// file operations. It tries, in order:
//
//  1. The currently-running process if it is already root (no IPC).
//  2. The native macOS Authorization Services dialog → __elevated-op re-exec
//     of cove with a typed manifest.
//
// The cove privileged helper daemon (helper.go) is an opt-in alternative for
// users who don't want a password prompt every time. It is NOT consulted by
// default: the trade-off is "always-on root daemon" vs "occasional dialog,"
// and we'd rather make users pick "always-on" deliberately. Set
// COVE_USE_HELPER=1 to route through the helper when installed.
//
// In restricted environments (Claude Code, sandboxed shells), the dialog
// cannot appear; the user is told how to run the equivalent command by hand
// and errRestrictedNoElevation is returned.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

var runElevatedManifestNativeHook = runElevatedManifestNative

// runElevated executes a manifest with root privileges. Prompt is shown to
// the user inside the AEWP dialog body (one short sentence).
func runElevated(m *elevatedManifest, prompt string) error {
	return runOffUIThread(func() error {
		return runElevatedOnCurrentThread(m, prompt)
	})
}

func runElevatedOnCurrentThread(m *elevatedManifest, prompt string) error {
	if os.Geteuid() == 0 {
		return runElevatedManifest(m)
	}

	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	// Helper daemon is opt-in only: it's an always-on root listener, which
	// is a meaningful security trade-off, so we don't reach for it
	// automatically just because the socket exists.
	if os.Getenv("COVE_USE_HELPER") == "1" {
		handled, herr := runManifestViaHelper(body)
		if handled {
			return herr
		}
		if herr != nil && !errors.Is(herr, errHelperUnavailable) {
			fmt.Fprintf(os.Stderr, "cove helper unreachable: %v\n", herr)
			fmt.Fprintln(os.Stderr, "Falling back to admin password prompt.")
		}
	}

	if restrictedEnvironment() {
		printManualElevationManifest(body, prompt)
		return errRestrictedNoElevation
	}

	// AEWP path: write the manifest to a tmp file the elevated child can
	// read after re-execing. Hash it with SHA-256 and pass the hash on the
	// command line; the child re-reads the file as root and refuses to act
	// if the hash doesn't match (TOCTOU defense).
	tmp, err := os.CreateTemp("", "cove-elev-manifest-*.json")
	if err != nil {
		return fmt.Errorf("create manifest tmp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("write manifest tmp: %w", err)
	}
	tmp.Close()
	// 0600 narrows local exposure; the child reads it as root regardless.
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		return fmt.Errorf("chmod manifest tmp: %w", err)
	}

	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])

	if err := runElevatedManifestNativeHook(tmp.Name(), hash, prompt); err != nil {
		if errors.Is(err, ErrAuthorizationNoTTY) {
			printManualElevationManifest(body, prompt)
		}
		return err
	}
	if !helperInstalled() {
		fmt.Fprintln(os.Stderr, "(tip: 'cove helper install' replaces this prompt with a one-time setup)")
	}
	return nil
}

func runOffUIThread(fn func() error) error {
	if fn == nil {
		return nil
	}
	if !onUIThread() {
		return fn()
	}
	done := make(chan error, 1)
	go func() {
		for onUIThread() {
			time.Sleep(time.Millisecond)
		}
		done <- fn()
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			drainUIThreadTasks()
		}
	}
}

// printManualElevationManifest tells the user how to apply the manifest by
// hand from a real terminal. We can't dump the JSON usefully, so we point
// them at `cove __elevated-op <path> <hash>` and leave the file in place
// for them to reuse.
func printManualElevationManifest(body []byte, prompt string) {
	tmp, err := os.CreateTemp("", "cove-elev-manifest-*.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot stage manifest for manual elevation: %v\n", err)
		return
	}
	tmp.Write(body)
	tmp.Close()
	os.Chmod(tmp.Name(), 0600)
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])

	exe, _ := os.Executable()
	if exe == "" {
		exe = "cove"
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Cannot show password dialog in this environment (sandboxed).")
	if prompt != "" {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "  "+prompt)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Open a normal Terminal window and rerun the cove command there.")
	fmt.Fprintln(os.Stderr, "That lets macOS show the administrator approval dialog.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "For future hands-off provisioning, install the helper once:")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  sudo cove helper install")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Advanced manual recovery, if the dialog still cannot appear:")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  sudo %s %s %s %s\n", exe, elevatedOpArg, tmp.Name(), hash)
	fmt.Fprintln(os.Stderr)
}
