package coved

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookPostSendsJSONAndReturnsNilOn2xx(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if got := r.Header.Get("content-type"); got != "application/json" {
			t.Errorf("content-type = %q, want application/json", got)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	wb := &WebhookSubscriber{URL: srv.URL, Client: srv.Client()}
	if err := wb.post(context.Background(), Event{EventType: "x"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}

func TestWebhookPost5xxReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	wb := &WebhookSubscriber{URL: srv.URL, Client: srv.Client()}
	if err := wb.post(context.Background(), Event{EventType: "x"}); err != nil {
		t.Fatalf("post: %v", err)
	}
}

func TestWebhookPostInvalidURLErrors(t *testing.T) {
	wb := &WebhookSubscriber{URL: "://bad-scheme"}
	if err := wb.post(context.Background(), Event{}); err == nil {
		t.Fatal("expected request error")
	}
}

func TestWebhookLastDeliveryUnixUpdatesOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	wb := &WebhookSubscriber{URL: srv.URL, Client: srv.Client()}
	if got := wb.LastDeliveryUnix(); got != 0 {
		t.Fatalf("LastDeliveryUnix initial = %d, want 0", got)
	}
	before := time.Now().Unix()
	wb.deliver(context.Background(), Event{EventType: "x"})
	got := wb.LastDeliveryUnix()
	if got < before {
		t.Fatalf("LastDeliveryUnix = %d, want >= %d", got, before)
	}
	if wb.Delivered() != 1 {
		t.Fatalf("Delivered = %d, want 1", wb.Delivered())
	}
}

func TestWebhookLastDeliveryUnixNilReturnsZero(t *testing.T) {
	var wb *WebhookSubscriber
	if got := wb.LastDeliveryUnix(); got != 0 {
		t.Fatalf("nil LastDeliveryUnix = %d, want 0", got)
	}
}

func TestWebhookPostFallsBackToDefaultClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()
	wb := &WebhookSubscriber{URL: srv.URL}
	if err := wb.post(context.Background(), Event{EventType: "x"}); err != nil {
		t.Fatalf("post: %v", err)
	}
}
