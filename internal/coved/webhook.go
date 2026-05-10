package coved

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type WebhookSubscriber struct {
	URL    string
	Events map[string]bool
	Client *http.Client

	delivered atomic.Uint64
	failed    atomic.Uint64
	rejected  atomic.Uint64
}

// Rejected returns the count of post() calls that completed with a
// non-2xx HTTP status. The current retry logic treats those as success
// (no retry, delivered++); rejected gives operators a separate signal
// so a misconfigured endpoint is visible without changing semantics.
func (w *WebhookSubscriber) Rejected() uint64 {
	if w == nil {
		return 0
	}
	return w.rejected.Load()
}

func (w *WebhookSubscriber) Delivered() uint64 {
	if w == nil {
		return 0
	}
	return w.delivered.Load()
}

func (w *WebhookSubscriber) Failed() uint64 {
	if w == nil {
		return 0
	}
	return w.failed.Load()
}

func NewWebhookSubscriber(cfg WebhookConfig) *WebhookSubscriber {
	if cfg.URL == "" {
		return nil
	}
	events := make(map[string]bool, len(cfg.Events))
	for _, event := range cfg.Events {
		events[event] = true
	}
	return &WebhookSubscriber{
		URL:    cfg.URL,
		Events: events,
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (w *WebhookSubscriber) Run(ctx context.Context, bus *EventBus) {
	if w == nil || bus == nil || w.URL == "" {
		return
	}
	ch, cancel := bus.Subscribe(64)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if !w.want(event.EventType) {
				continue
			}
			go w.deliver(ctx, event)
		}
	}
}

func (w *WebhookSubscriber) want(eventType string) bool {
	return len(w.Events) == 0 || w.Events[eventType]
}

func (w *WebhookSubscriber) deliver(ctx context.Context, event Event) {
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return
		}
		err := w.post(ctx, event)
		if err == nil {
			w.delivered.Add(1)
			return
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	w.failed.Add(1)
}

func (w *WebhookSubscriber) post(ctx context.Context, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("webhook marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	client := w.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		w.rejected.Add(1)
	}
	return nil
}
