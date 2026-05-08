// Package sckit hosts the design 041 ScreenCaptureKit migration spike.
//
// CaptureSpike captures a single frame of a window via SCScreenshotManager
// and reports the wall-clock latency. It exists to validate the 50ms
// median threshold from design 041 Q3 before any production wiring.
package sckit

import (
	"context"
	"fmt"
	"image"
	"time"

	"github.com/tmc/apple/screencapturekit"
	"github.com/tmc/apple/x/vzkit/capture"
)

// CaptureSpike grabs a single screenshot of the window with the given
// CGWindowID using SCScreenshotManager and returns the decoded image
// plus the elapsed time. It returns an error if the window is not
// visible to the current process or if ScreenCaptureKit refuses (TCC).
func CaptureSpike(ctx context.Context, windowID uint32) (image.Image, time.Duration, error) {
	start := time.Now()

	content, err := screencapturekit.GetSCShareableContentClass().GetShareableContentExcludingDesktopWindowsOnScreenWindowsOnly(ctx, true, true)
	if err != nil {
		return nil, 0, fmt.Errorf("shareable content: %w", err)
	}
	if content == nil {
		return nil, 0, fmt.Errorf("shareable content: nil result")
	}
	var target screencapturekit.SCWindow
	var found bool
	for _, w := range content.Windows() {
		if w.WindowID() == windowID {
			target = w
			found = true
			break
		}
	}
	if !found {
		return nil, 0, fmt.Errorf("window %d not in shareable content (TCC denied or off-screen)", windowID)
	}

	filter := screencapturekit.NewContentFilterWithDesktopIndependentWindow(target)
	cfg := screencapturekit.NewSCStreamConfiguration()
	cfg.SetShowsCursor(false)

	cgImage, err := screencapturekit.GetSCScreenshotManagerClass().CaptureImageWithFilterConfiguration(ctx, filter, cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("captureImage: %w", err)
	}
	if cgImage == 0 {
		return nil, 0, fmt.Errorf("captureImage: nil image")
	}
	img, err := capture.GoImageFromCGImage(cgImage, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("decode: %w", err)
	}
	return img, time.Since(start), nil
}
