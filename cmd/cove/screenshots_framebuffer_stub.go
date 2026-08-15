//go:build !darwin

package main

import "image"

func (s *ControlServer) captureVZFramebuffer() (image.Image, string) {
	return nil, "framebuffer screenshot backend requires darwin"
}
