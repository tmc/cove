// sckit_preauth.go - `cove doctor sckit-preauth` diagnostic.
//
// Reports ScreenCaptureKit availability and the Screen Recording TCC
// state without prompting the user. Exit code 0 when both flags are
// true, 1 otherwise. Slice 1 of design 041; no production capture
// path consumes the probe yet.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tmc/vz-macos/internal/sckit"
)

func runSCKitPreAuth(args []string) error {
	fs := flag.NewFlagSet("doctor sckit-preauth", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "Emit the probe as JSON")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: cove doctor sckit-preauth [-json]

Report ScreenCaptureKit availability and the host Screen Recording
permission. Read-only: never triggers a TCC prompt. Exit 0 if SCKit
is available and screen recording is authorized, 1 otherwise.

Options:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	probe := sckit.Detect()
	return reportSCKitProbe(os.Stdout, probe, *jsonOut)
}

func reportSCKitProbe(w io.Writer, p sckit.Probe, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(p); err != nil {
			return err
		}
	} else {
		writeSCKitTable(w, p)
	}
	if p.SCKitAvailable && p.ScreenRecordingAuthorized {
		return nil
	}
	return errors.New("screen recording not authorized; grant in System Settings > Privacy & Security > Screen Recording")
}

func writeSCKitTable(w io.Writer, p sckit.Probe) {
	version := p.MacOSVersion
	if version == "" {
		version = "(unknown)"
	}
	fmt.Fprintln(w, "=== cove ScreenCaptureKit pre-flight ===")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  macOS version              : %s\n", version)
	fmt.Fprintf(w, "  SCKit available            : %s\n", boolMark(p.SCKitAvailable))
	fmt.Fprintf(w, "  screen recording authorized: %s\n", boolMark(p.ScreenRecordingAuthorized))
	fmt.Fprintln(w)
}

func boolMark(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
