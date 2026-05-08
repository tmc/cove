//go:build !darwin

package sckit

// Detect on non-darwin always reports unavailable.
func Detect() Probe {
	return Probe{}
}
