// screen_capture.go - Capture/screenshot/OCR state owned by ControlServer.
package main

import (
	"image"
	"sync"

	"github.com/tmc/apple/x/vzkit/capture"
	ocrx "github.com/tmc/apple/x/vzkit/ocr"
)

// screenCapture owns the diff cache and the lazy OCR service used by
// ControlServer's capture and OCR paths. All state is guarded by mu.
type screenCapture struct {
	mu    sync.Mutex
	last  image.Image
	lastW int
	lastH int
	ocr   *ocrx.Service
}

// rememberBounds records the dimensions of the most recent capture so
// later input mapping can translate OCR coordinates back to view points.
func (c *screenCapture) rememberBounds(img image.Image) {
	if img == nil {
		return
	}
	b := img.Bounds()
	c.mu.Lock()
	c.lastW = b.Dx()
	c.lastH = b.Dy()
	c.mu.Unlock()
}

// lastBounds returns the dimensions of the most recent capture, or 0,0
// if none has been recorded.
func (c *screenCapture) lastBounds() (width, height int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastW, c.lastH
}

// diff returns either img or its diff against the previous capture (when
// enabled and a previous capture exists), and stores img as the new
// previous capture.
func (c *screenCapture) diff(img image.Image, enable bool) image.Image {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := img
	if enable && c.last != nil {
		out = capture.GenerateDiff(c.last, img)
	}
	c.last = img
	return out
}

// service returns the OCR service, creating it on first use.
func (c *screenCapture) service(verbose bool) *ocrx.Service {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ocr == nil {
		c.ocr = ocrx.NewService(verbose)
	}
	return c.ocr
}
