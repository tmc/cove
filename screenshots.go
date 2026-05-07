// screenshots.go - Screenshot capture, diff, and compression for VM control.
//
// Generic image utilities delegate to github.com/tmc/apple/x/vzkit/capture.
// ControlServer methods remain here since they depend on VM-specific state.
package main

import (
	"encoding/base64"
	"fmt"
	"image"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/capture"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type screenshotCaptureState struct {
	vmView            vz.VZVirtualMachineView
	window            appkit.NSWindow
	windowNum         int
	viewContentHeight int
	gui               VMGUIController
}

func (s *ControlServer) captureState() screenshotCaptureState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return screenshotCaptureState{
		vmView:            s.vmView,
		window:            s.window,
		windowNum:         s.windowNum,
		viewContentHeight: s.viewContentHeight,
		gui:               s.gui,
	}
}

// takeScreenshotWithOptions captures the VM view with specified options.
func (s *ControlServer) takeScreenshotWithOptions(opts *controlpb.ScreenshotCommand) *controlpb.ControlResponse {
	state := s.captureState()
	if state.vmView.ID == 0 {
		return &controlpb.ControlResponse{Error: "screenshot requires GUI mode (run with -gui)"}
	}

	scale := opts.Scale
	if scale <= 0 || scale > 1 {
		scale = 0.5
	}
	quality := opts.Quality
	if quality <= 0 || quality > 100 {
		quality = 60
	}
	format := opts.Format
	if format == "" {
		format = "jpeg"
	}

	img, errMsg := s.captureDisplayImage()
	if errMsg != "" {
		return &controlpb.ControlResponse{Error: errMsg}
	}

	outputImg := s.capture.diff(img, opts.Diff)

	if scale < 1 {
		outputImg = capture.ScaleImage(outputImg, scale)
	}

	var rawBytes []byte
	var err error
	if format == "png" {
		rawBytes, err = capture.EncodePNG(outputImg)
	} else {
		rawBytes, err = capture.EncodeJPEG(outputImg, int(quality))
	}
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	encoded := base64.StdEncoding.EncodeToString(rawBytes)
	bounds := outputImg.Bounds()
	return &controlpb.ControlResponse{
		Success: true,
		Data:    encoded,
		Result: &controlpb.ControlResponse_ScreenshotResult{ScreenshotResult: &controlpb.ScreenshotResponse{
			ImageData: rawBytes,
			Format:    format,
			Width:     int32(bounds.Dx()),
			Height:    int32(bounds.Dy()),
		}},
	}
}

func (s *ControlServer) captureDisplayImage() (image.Image, string) {
	state := s.captureState()
	remember := func(img image.Image, errMsg string) (image.Image, string) {
		if errMsg == "" {
			s.rememberCaptureBounds(img)
		}
		return img, errMsg
	}
	switch s.captureBackend() {
	case automationBackendFramebuffer:
		return remember(s.capturePrivateGraphicsDisplay())
	case automationBackendWindow:
		return remember(s.captureVMView())
	}

	if state.gui != nil {
		status := state.gui.Status()
		if !status.Headed {
			if img, errMsg := s.capturePrivateGraphicsDisplay(); errMsg == "" {
				return remember(img, "")
			} else {
				if verbose {
					fmt.Printf("[screenshot] private capture unavailable: %s\n", errMsg)
				}
				return remember(nil, errMsg)
			}
		}
	}
	return remember(s.captureVMView())
}

// captureVMView captures the raw image from the VM view using CGWindowListCreateImage.
func (s *ControlServer) captureVMView() (image.Image, string) {
	state := s.captureState()
	if state.window.ID == 0 {
		return nil, "window not set"
	}

	windowNum := state.windowNum
	if windowNum <= 0 {
		return nil, fmt.Sprintf("invalid window number: %d", windowNum)
	}

	bounds := corefoundation.CGRect{} // CGRectNull
	cgImage := coregraphics.CGWindowListCreateImage(
		bounds,
		coregraphics.CGWindowListOption(8), // kCGWindowListOptionIncludingWindow
		coregraphics.CGWindowID(windowNum),
		coregraphics.CGWindowImageOption(1), // kCGWindowImageBoundsIgnoreFraming
	)

	if cgImage == 0 {
		if verbose {
			fmt.Printf("[screenshot] CGWindowListCreateImage returned nil for windowNum=%d\n", windowNum)
		}
		return nil, "CGWindowListCreateImage returned nil"
	}
	defer coregraphics.CGImageRelease(cgImage)

	// Do not blindly crop the top delta between the captured window image and
	// the VM view bounds. Recovery can render guest-visible controls in that
	// strip, including the menu bar needed for Terminal automation.
	//
	// The old behavior assumed the delta was always host title bar chrome.
	// Live Recovery captures show that assumption is false.
	img, err := capture.GoImageFromCGImage(cgImage, 0)
	if err != nil {
		return nil, err.Error()
	}
	return img, ""
}
