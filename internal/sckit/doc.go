// Package sckit probes the host for ScreenCaptureKit availability.
//
// Slice 1 of design 041 ships only the diagnostic surface: callers can
// ask whether SCKit is present and whether the screen-recording TCC
// permission has been granted, without triggering any consent prompt.
// No production capture path uses this package yet.
package sckit
