// Capture holds the diff cache and the lazy OCR service used by
// the control server's capture and OCR paths.
package controlserver

import (
	"context"
	"image"
	"sync"

	"github.com/tmc/apple/x/capture"
	ocrx "github.com/tmc/apple/x/vzkit/ocr"
)

// Capture owns the diff cache and the lazy OCR service used by the
// control server. All state is guarded by mu. The zero value is
// usable.
type Capture struct {
	mu      sync.Mutex
	last    image.Image
	lastW   int
	lastH   int
	ocr     *ocrx.Service
	metrics CaptureMetrics
}

// SetMetrics sets the sink used for capture-path latency measurements.
func (c *Capture) SetMetrics(metrics CaptureMetrics) {
	c.mu.Lock()
	c.metrics = metrics
	c.mu.Unlock()
}

// EmitCaptureLatency forwards e to the configured metrics sink, if any.
func (c *Capture) EmitCaptureLatency(ctx context.Context, e CaptureLatencyEvent) {
	c.mu.Lock()
	metrics := c.metrics
	c.mu.Unlock()
	if metrics != nil {
		metrics.EmitCaptureLatency(ctx, e)
	}
}

// RememberBounds records the dimensions of the most recent capture so
// later input mapping can translate OCR coordinates back to view points.
func (c *Capture) RememberBounds(img image.Image) {
	if img == nil {
		return
	}
	b := img.Bounds()
	c.mu.Lock()
	c.lastW = b.Dx()
	c.lastH = b.Dy()
	c.mu.Unlock()
}

// LastBounds returns the dimensions of the most recent capture, or 0,0
// if none has been recorded.
func (c *Capture) LastBounds() (width, height int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastW, c.lastH
}

// Diff returns either img or its diff against the previous capture
// (when enabled and a previous capture exists), and stores img as the
// new previous capture.
func (c *Capture) Diff(img image.Image, enable bool) image.Image {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := img
	if enable && c.last != nil {
		out = capture.GenerateDiff(c.last, img)
	}
	c.last = img
	return out
}

// Service returns the OCR service, creating it on first use.
func (c *Capture) Service(verbose bool) *ocrx.Service {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ocr == nil {
		c.ocr = ocrx.NewService(verbose)
	}
	return c.ocr
}
