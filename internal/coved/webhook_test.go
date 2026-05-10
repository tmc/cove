package coved

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookSubscriberFiltersAndPosts(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Add(1)
		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("decode: %v", err)
		}
		if event.EventType != "image.gc.run" {
			t.Errorf("event = %q", event.EventType)
		}
	}))
	defer srv.Close()
	bus := NewEventBus(4)
	sub := NewWebhookSubscriber(WebhookConfig{URL: srv.URL, Events: []string{"image.gc.run"}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.Run(ctx, bus)
	waitSubscribers(t, bus)
	bus.Publish(ctx, Event{EventType: "lifecycle.policy.stop"})
	bus.Publish(ctx, Event{EventType: "image.gc.run"})
	deadline := time.After(time.Second)
	for got.Load() != 1 {
		select {
		case <-deadline:
			t.Fatalf("posts = %d, want 1", got.Load())
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestWebhookSubscriberDoesNotBlockPublish(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	bus := NewEventBus(4)
	sub := NewWebhookSubscriber(WebhookConfig{URL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.Run(ctx, bus)
	waitSubscribers(t, bus)
	done := make(chan struct{})
	go func() {
		bus.Publish(ctx, Event{EventType: "image.gc.run"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Publish blocked on webhook")
	}
}

func TestWebhookSubscriberCountsDeliveredAndFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := NewWebhookSubscriber(WebhookConfig{URL: srv.URL})
	w.deliver(context.Background(), Event{EventType: "ok"})
	if w.Delivered() != 1 {
		t.Fatalf("Delivered = %d, want 1", w.Delivered())
	}

	bad := &WebhookSubscriber{
		URL:    "http://127.0.0.1:1",
		Client: &http.Client{Timeout: 50 * time.Millisecond},
	}
	bad.deliver(context.Background(), Event{EventType: "x"})
	if bad.Failed() != 1 {
		t.Fatalf("Failed = %d, want 1", bad.Failed())
	}
}

func waitSubscribers(t *testing.T, bus *EventBus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for bus.subscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscriber did not start")
		}
		time.Sleep(time.Millisecond)
	}
}
