// Package sckit probes the host for ScreenCaptureKit availability and
// exposes a single-window capture entry point used by design 041.
//
// Slice 1 shipped the diagnostic surface (Detect, used by
// cove doctor sckit-preauth). Slice 2 added CaptureSpike, the timing
// harness behind cove doctor sckit-spike. Slice 3 wires CaptureWindow
// into the production capture path on an opt-in basis. Slice 4 makes
// CaptureWindow the production capture path.
//
// The live SCKit smoke test lives in files tagged
// `//go:build darwin && sckit_live` and is gated on
// COVE_TEST_SCKIT_GRANT=1 so CI never triggers a TCC prompt.
package sckit
