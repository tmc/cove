//go:build !darwin

package main

import "image"

func (s *ControlServer) capturePrivateGraphicsDisplay() (image.Image, string) {
	return nil, "private graphics capture unavailable"
}
