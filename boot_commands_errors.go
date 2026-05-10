package main

import "errors"

// ErrBootTimeout is returned by the unattended-setup OCR helpers
// (waitForText, clickText, hostClickText) when the deadline lapses
// before the target text appears on the captured screen. Callers can
// branch on this with errors.Is to surface a "screen never appeared"
// hint vs other automation failures (control server gone, image
// capture errored, etc.).
var ErrBootTimeout = errors.New("boot automation timed out")
