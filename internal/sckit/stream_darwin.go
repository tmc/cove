//go:build darwin

package sckit

import (
	"context"
	"errors"
	"fmt"
	"image"
	"time"
	"unsafe"

	"github.com/tmc/apple/coremedia"
	"github.com/tmc/apple/corevideo"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/screencapturekit"
)

// StartStream begins a persistent ScreenCaptureKit capture of the
// window with the given CGWindowID and returns a Stream whose Snapshot
// serves the most recent frame. Frames arrive only when the window
// content changes; until the first repaint Snapshot returns ErrNoFrame,
// so callers wanting an immediate image should fall back to
// CaptureWindow for the first frame.
//
// StartStream requires Screen Recording authorization (TCC); it fails
// with an SCKit error when the grant is missing.
func StartStream(ctx context.Context, windowID uint32) (*Stream, error) {
	content, err := screencapturekit.GetSCShareableContentClass().GetShareableContentExcludingDesktopWindowsOnScreenWindowsOnly(ctx, true, true)
	if err != nil {
		return nil, fmt.Errorf("shareable content: %w", err)
	}
	if content == nil {
		return nil, fmt.Errorf("shareable content: nil result")
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
		return nil, fmt.Errorf("window %d not in shareable content: %w", windowID, ErrWindowNotFound)
	}

	filter := screencapturekit.NewContentFilterWithDesktopIndependentWindow(target)
	cfg := screencapturekit.NewSCStreamConfiguration()
	cfg.SetShowsCursor(false)
	cfg.SetPixelFormat(uint32(corevideo.KCVPixelFormatType_32BGRA))
	cfg.SetQueueDepth(3)
	if frame := target.Frame(); frame.Size.Width >= 1 && frame.Size.Height >= 1 {
		cfg.SetWidth(uintptr(frame.Size.Width))
		cfg.SetHeight(uintptr(frame.Size.Height))
	}

	s := &Stream{}
	delegate := screencapturekit.NewSCStreamDelegate(screencapturekit.SCStreamDelegateConfig{
		StreamDidStopWithError: func(_ screencapturekit.SCStream, nserr foundation.NSError) {
			s.noteStop(fmt.Errorf("sckit: stream stopped: %s", nserr.LocalizedDescription()))
		},
	})
	output := screencapturekit.NewSCStreamOutput(screencapturekit.SCStreamOutputConfig{
		StreamDidOutputSampleBufferOfType: func(_ screencapturekit.SCStream, sb coremedia.CMSampleBufferRef, type_ screencapturekit.SCStreamOutputType) {
			if type_ != screencapturekit.SCStreamOutputTypeScreen {
				return
			}
			if img, ok := imageFromSampleBuffer(sb); ok {
				s.storeFrame(img, time.Now())
			}
		},
	})
	queue := dispatch.QueueCreate("com.tmc.cove.sckit.stream")

	stream := screencapturekit.NewStreamWithFilterConfigurationDelegate(filter, cfg, delegate)
	if stream.ID == 0 {
		return nil, errors.New("sckit: SCStream init failed")
	}
	if ok, err := stream.AddStreamOutputTypeSampleHandlerQueueError(output, screencapturekit.SCStreamOutputTypeScreen, queue); err != nil || !ok {
		if err == nil {
			err = errors.New("addStreamOutput refused")
		}
		return nil, fmt.Errorf("add stream output: %w", err)
	}
	if err := stream.StartCapture(ctx); err != nil {
		return nil, fmt.Errorf("startCapture: %w", err)
	}
	s.stopFn = func(ctx context.Context) error {
		return stream.StopCapture(ctx)
	}
	s.keepAlive = []any{delegate, output, queue, stream, filter, cfg}
	return s, nil
}

// imageFromSampleBuffer converts a 32BGRA sample buffer into an RGBA
// image, copying the pixels out so the buffer can be released.
func imageFromSampleBuffer(sb coremedia.CMSampleBufferRef) (image.Image, bool) {
	if !coremedia.CMSampleBufferIsValid(sb) {
		return nil, false
	}
	buf := corevideo.CVPixelBufferRef(coremedia.CMSampleBufferGetImageBuffer(sb))
	if buf == 0 {
		return nil, false
	}
	if corevideo.CVPixelBufferGetPixelFormatType(buf) != uint32(corevideo.KCVPixelFormatType_32BGRA) {
		return nil, false
	}
	if corevideo.CVPixelBufferLockBaseAddress(buf, corevideo.KCVPixelBufferLock_ReadOnly) != 0 {
		return nil, false
	}
	defer corevideo.CVPixelBufferUnlockBaseAddress(buf, corevideo.KCVPixelBufferLock_ReadOnly)

	base := corevideo.CVPixelBufferGetBaseAddress(buf)
	if base == nil {
		return nil, false
	}
	width := int(corevideo.CVPixelBufferGetWidth(buf))
	height := int(corevideo.CVPixelBufferGetHeight(buf))
	stride := int(corevideo.CVPixelBufferGetBytesPerRow(buf))
	if width <= 0 || height <= 0 || stride < width*4 {
		return nil, false
	}
	src := unsafe.Slice((*byte)(base), height*stride)
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srcRow := src[y*stride:]
		dstRow := rgba.Pix[y*rgba.Stride:]
		for x := 0; x < width; x++ {
			// BGRA -> RGBA
			dstRow[x*4+0] = srcRow[x*4+2]
			dstRow[x*4+1] = srcRow[x*4+1]
			dstRow[x*4+2] = srcRow[x*4+0]
			dstRow[x*4+3] = srcRow[x*4+3]
		}
	}
	return rgba, true
}
