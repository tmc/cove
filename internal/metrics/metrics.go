package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const otlpEndpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"

// Event is one metrics record.
type Event struct {
	Timestamp  string         `json:"timestamp"`
	EventType  string         `json:"event_type"`
	VMName     string         `json:"vm_name,omitempty"`
	ImageRef   string         `json:"image_ref,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Status     string         `json:"status,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// Sink records metrics events.
type Sink interface {
	Emit(context.Context, Event) error
	Close() error
}

// NewSink returns a JSONL sink writing to w. When OTEL_EXPORTER_OTLP_ENDPOINT
// is set, it also posts events to that endpoint.
func NewSink(w io.Writer) Sink {
	endpoint := os.Getenv(otlpEndpointEnv)
	if endpoint != "" {
		return MultiSink{NewJSONL(w), NewOTLPSink(endpoint)}
	}
	return NewJSONL(w)
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(context.Context, Event) error

// Emit calls f(ctx, e).
func (f SinkFunc) Emit(ctx context.Context, e Event) error {
	return f(ctx, e)
}

// Close is a no-op.
func (f SinkFunc) Close() error { return nil }

// JSONL writes one JSON event per line.
type JSONL struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONL returns a JSONL sink writing to w.
func NewJSONL(w io.Writer) *JSONL {
	return &JSONL{w: w}
}

// Emit writes e as a single JSON line.
func (j *JSONL) Emit(ctx context.Context, e Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if j == nil || j.w == nil {
		return fmt.Errorf("metrics jsonl: nil writer")
	}
	if e.Timestamp == "" {
		e.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.Timestamp); err != nil {
		return fmt.Errorf("metrics jsonl: invalid timestamp: %w", err)
	}

	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("metrics jsonl: marshal event: %w", err)
	}
	b = append(b, '\n')

	j.mu.Lock()
	defer j.mu.Unlock()
	if _, err := j.w.Write(b); err != nil {
		return fmt.Errorf("metrics jsonl: write event: %w", err)
	}
	return nil
}

// Close is a no-op for writer-backed JSONL sinks.
func (j *JSONL) Close() error { return nil }

// JSONLSink appends JSONL events to a file.
type JSONLSink struct {
	mu sync.Mutex
	f  *os.File
}

// NewJSONLSink opens path for append, creating parent directories.
func NewJSONLSink(path string) (*JSONLSink, error) {
	if path == "" {
		return nil, fmt.Errorf("metrics jsonl: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("metrics jsonl: create dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("metrics jsonl: open file: %w", err)
	}
	return &JSONLSink{f: f}, nil
}

// Emit writes e as a single JSON line.
func (j *JSONLSink) Emit(ctx context.Context, e Event) error {
	if j == nil {
		return fmt.Errorf("metrics jsonl: nil sink")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return fmt.Errorf("metrics jsonl: sink closed")
	}
	return NewJSONL(j.f).Emit(ctx, e)
}

// Close closes the sink file.
func (j *JSONLSink) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

// MultiSink emits each event to several sinks.
type MultiSink []Sink

// Emit sends e to each sink, returning the first error.
func (m MultiSink) Emit(ctx context.Context, e Event) error {
	for _, s := range m {
		if s == nil {
			continue
		}
		if err := s.Emit(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

// Close closes each sink, returning the first error.
func (m MultiSink) Close() error {
	var first error
	for _, s := range m {
		if s == nil {
			continue
		}
		if err := s.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// OTLPSink posts events as JSON to an OTLP-compatible HTTP endpoint.
type OTLPSink struct {
	Endpoint string
	Client   *http.Client
}

// NewOTLPSink returns an HTTP OTLP sink.
func NewOTLPSink(endpoint string) *OTLPSink {
	return &OTLPSink{
		Endpoint: endpoint,
		Client:   &http.Client{Timeout: 5 * time.Second},
	}
}

// Emit posts e to the configured endpoint.
func (o *OTLPSink) Emit(ctx context.Context, e Event) error {
	if o == nil || o.Endpoint == "" {
		return nil
	}
	if e.Timestamp == "" {
		e.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("metrics otlp: marshal event: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("metrics otlp: build request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("metrics otlp: post event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		body = bytes.TrimSpace(body)
		if len(body) > 0 {
			return fmt.Errorf("metrics otlp: status %s: %s", resp.Status, body)
		}
		return fmt.Errorf("metrics otlp: status %s", resp.Status)
	}
	return nil
}

// Close is a no-op.
func (o *OTLPSink) Close() error { return nil }
