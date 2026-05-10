package coved

import (
	"context"
	"sync"
	"testing"
	"time"

	runmetrics "github.com/tmc/vz-macos/internal/metrics"
)

func TestEventBusPublishSubscribeTail(t *testing.T) {
	bus := NewEventBus(2)
	ch, cancel := bus.Subscribe(2)
	defer cancel()
	bus.Publish(context.Background(), Event{EventType: "one"})
	bus.Publish(context.Background(), Event{EventType: "two"})
	bus.Publish(context.Background(), Event{EventType: "three"})
	for _, want := range []string{"one", "two"} {
		select {
		case got := <-ch:
			if got.EventType != want {
				t.Fatalf("event = %q, want %q", got.EventType, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
	tail := bus.Tail()
	if len(tail) != 2 || tail[0].EventType != "two" || tail[1].EventType != "three" {
		t.Fatalf("tail = %#v", tail)
	}
}

func TestEventBusDoesNotBlockOnSlowSubscriber(t *testing.T) {
	bus := NewEventBus(4)
	_, cancel := bus.Subscribe(1)
	defer cancel()
	bus.Publish(context.Background(), Event{EventType: "one"})
	done := make(chan struct{})
	go func() {
		bus.Publish(context.Background(), Event{EventType: "two"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on full subscriber")
	}
}

func TestJSONLSubscriberReceivesEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := NewEventBus(4)
	sink := &recordSink{}
	go RunJSONLSubscriber(ctx, bus, sink)
	for deadline := time.Now().Add(time.Second); bus.subscriberCount() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("subscriber did not start")
		}
		time.Sleep(time.Millisecond)
	}
	bus.Publish(ctx, Event{EventType: "image.gc.run"})
	deadline := time.After(time.Second)
	for {
		if sink.len() == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("events = %d, want 1", sink.len())
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

type recordSink struct {
	mu     sync.Mutex
	events []runmetrics.Event
}

func (s *recordSink) Emit(ctx context.Context, e runmetrics.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *recordSink) Close() error { return nil }

func (s *recordSink) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func TestEventBusDroppedCountsBackpressure(t *testing.T) {
	bus := NewEventBus(8)
	ch, cancel := bus.Subscribe(1)
	defer cancel()

	for i := 0; i < 5; i++ {
		bus.Publish(context.Background(), Event{EventType: "x"})
	}
	if got := bus.Dropped(); got < 4 {
		t.Fatalf("Dropped = %d, want >= 4 (only 1 buffered)", got)
	}
	<-ch
}
