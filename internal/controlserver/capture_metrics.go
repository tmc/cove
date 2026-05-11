package controlserver

import "context"

// CaptureLatencyEvent describes one completed screenshot capture attempt.
type CaptureLatencyEvent struct {
	Backend          string
	RequestedBackend string
	Fallback         bool
	FallbackCause    string
	Width            int
	Height           int
	DurationMS       int64
	Status           string
	Error            string
}

// CaptureMetrics receives capture-path latency measurements.
type CaptureMetrics interface {
	EmitCaptureLatency(context.Context, CaptureLatencyEvent)
}
