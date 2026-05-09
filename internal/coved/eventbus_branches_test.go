package coved

import (
	"context"
	"testing"
)

func TestNewEventBusDefaultsTailSize(t *testing.T) {
	tests := []struct {
		name     string
		tailSize int
		want     int
	}{
		{name: "zero defaults to 50", tailSize: 0, want: 50},
		{name: "negative defaults to 50", tailSize: -1, want: 50},
		{name: "positive preserved", tailSize: 7, want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewEventBus(tt.tailSize)
			if b.size != tt.want {
				t.Fatalf("size = %d, want %d", b.size, tt.want)
			}
		})
	}
}

func TestSubscribeDefaultBuffer(t *testing.T) {
	b := NewEventBus(10)
	ch, cancel := b.Subscribe(0)
	defer cancel()
	if cap(ch) != 16 {
		t.Fatalf("Subscribe(0) channel cap = %d, want 16 (default)", cap(ch))
	}
}

func TestNilEventBusOpsAreSafe(t *testing.T) {
	var b *EventBus
	// Publish, Tail, subscriberCount must all be no-ops on nil receiver.
	b.Publish(context.Background(), Event{})
	if got := b.Tail(); got != nil {
		t.Fatalf("nil bus Tail() = %#v, want nil", got)
	}
	if got := b.subscriberCount(); got != 0 {
		t.Fatalf("nil bus subscriberCount() = %d, want 0", got)
	}
}

func TestRunJSONLSubscriberNilGuards(t *testing.T) {
	// Both arms must early-return without panicking.
	RunJSONLSubscriber(context.Background(), nil, nil)
	bus := NewEventBus(2)
	RunJSONLSubscriber(context.Background(), bus, nil)
}
