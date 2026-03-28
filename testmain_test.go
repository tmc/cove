//go:build darwin && arm64

package main

import (
	"flag"
	"os"
	"testing"

	"github.com/tmc/vz-macos/internal/autosign"
)

func TestMain(m *testing.M) {
	if err := autosign.EnsureEntitlements(); err != nil {
		// EnsureEntitlements re-execs on success, so reaching here means
		// either we already have entitlements or signing failed.
		// Log and continue — tests that need entitlements will fail with
		// clear errors rather than crashing.
		os.Stderr.WriteString("warning: autosign: " + err.Error() + "\n")
	}
	flag.Parse()
	os.Exit(m.Run())
}
