// screenshots.go - Screenshot capture, diff, and compression for VM control.
//
// Generic image utilities delegate to github.com/tmc/apple/x/capture.
// ControlServer methods remain here since they depend on VM-specific state.

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"time"

	"github.com/tmc/apple/appkit"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/capture"

	"github.com/tmc/cove/internal/controlserver"
	"github.com/tmc/cove/internal/sckit"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

// captureSCKitFn is the seam used by captureVMView. Tests override it
// without requiring a TCC-granted host.
var captureSCKitFn = func(ctx context.Context, windowID uint32) (image.Image, error) {
	return sckit.CaptureWindow(ctx, windowID)
}

type screenshotCaptureState struct {
	vmView            vz.VZVirtualMachineView
	window            appkit.NSWindow
	windowNum         int
	viewContentHeight int
	gui               VMGUIController
}

type captureDisplayResult struct {
	img              image.Image
	errMsg           string
	backend          string
	requestedBackend string
	fallback         bool
	fallbackCause    string
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

	outputImg := s.capture.Diff(img, opts.Diff)

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
	started := time.Now()
	result := s.captureDisplayImageResult()
	s.emitCaptureLatency(context.Background(), started, result)
	if result.errMsg == "" {
		s.rememberCaptureBounds(result.img)
	}
	return result.img, result.errMsg
}

func (s *ControlServer) captureDisplayImageResult() captureDisplayResult {
	state := s.captureState()
	switch s.captureBackend() {
	case automationBackendFramebuffer:
		img, errMsg := s.capturePrivateGraphicsDisplay()
		return captureDisplayResult{
			img:              img,
			errMsg:           errMsg,
			backend:          "framebuffer",
			requestedBackend: "framebuffer",
		}
	case automationBackendWindow:
		return s.captureVMViewResult("window")
	}

	if state.gui != nil {
		status := state.gui.Status()
		if !status.Headed {
			if img, errMsg := s.capturePrivateGraphicsDisplay(); errMsg == "" {
				return captureDisplayResult{
					img:              img,
					backend:          "framebuffer",
					requestedBackend: "auto",
				}
			} else {
				if verbose {
					fmt.Printf("[screenshot] private capture unavailable: %s\n", errMsg)
				}
				return captureDisplayResult{
					errMsg:           errMsg,
					backend:          "framebuffer",
					requestedBackend: "auto",
				}
			}
		}
	}
	return s.captureVMViewResult(string(sckit.BackendForVMDir(s.VMDir())))
}

func (s *ControlServer) emitCaptureLatency(ctx context.Context, started time.Time, result captureDisplayResult) {
	status := "ok"
	if result.errMsg != "" {
		status = "error"
	}
	width, height := captureImageSize(result.img)
	s.capture.EmitCaptureLatency(ctx, controlserver.CaptureLatencyEvent{
		Backend:          result.backend,
		RequestedBackend: result.requestedBackend,
		Fallback:         result.fallback,
		FallbackCause:    result.fallbackCause,
		Width:            width,
		Height:           height,
		DurationMS:       time.Since(started).Milliseconds(),
		Status:           status,
		Error:            truncateCaptureMetricError(result.errMsg),
	})
}

func captureImageSize(img image.Image) (int, int) {
	if img == nil {
		return 0, 0
	}
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

func truncateCaptureMetricError(msg string) string {
	if len(msg) <= 256 {
		return msg
	}
	return msg[:256]
}

// captureVMView captures the raw image from the VM view using ScreenCaptureKit.
func (s *ControlServer) captureVMView() (image.Image, string) {
	result := s.captureVMViewResult(string(sckit.BackendForVMDir(s.VMDir())))
	return result.img, result.errMsg
}

func (s *ControlServer) captureVMViewResult(requestedBackend string) captureDisplayResult {
	state := s.captureState()
	if requestedBackend == "" {
		requestedBackend = string(sckit.BackendForVMDir(s.VMDir()))
	}
	if state.window.ID == 0 {
		return captureDisplayResult{
			errMsg:           "window not set",
			backend:          "sckit",
			requestedBackend: requestedBackend,
		}
	}

	windowNum := state.windowNum
	if windowNum <= 0 {
		return captureDisplayResult{
			errMsg:           fmt.Sprintf("invalid window number: %d", windowNum),
			backend:          "sckit",
			requestedBackend: requestedBackend,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	img, err := captureSCKitFn(ctx, uint32(windowNum))
	cancel()
	if err != nil {
		return captureDisplayResult{
			errMsg:           err.Error(),
			backend:          "sckit",
			requestedBackend: requestedBackend,
		}
	}
	if img == nil {
		return captureDisplayResult{
			errMsg:           "sckit returned nil image",
			backend:          "sckit",
			requestedBackend: requestedBackend,
		}
	}
	return captureDisplayResult{
		img:              img,
		backend:          "sckit",
		requestedBackend: requestedBackend,
	}
}
