package coved

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	runmetrics "github.com/tmc/vz-macos/internal/metrics"
)

type Event = runmetrics.Event

type EventBus struct {
	mu   sync.RWMutex
	subs []chan Event
	tail []Event
	size int
}

func NewJSONLSink(path string) (runmetrics.Sink, error) {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".vz", "metrics.jsonl")
	}
	return runmetrics.NewJSONLSink(path)
}

func NewEventBus(tailSize int) *EventBus {
	if tailSize <= 0 {
		tailSize = 50
	}
	return &EventBus{size: tailSize}
}

func (b *EventBus) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 16
	}
	ch := make(chan Event, buffer)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, sub := range b.subs {
			if sub == ch {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, cancel
}

func (b *EventBus) Publish(ctx context.Context, e Event) {
	if b == nil {
		return
	}
	if e.Timestamp == "" {
		e.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b.mu.Lock()
	b.tail = append(b.tail, e)
	if len(b.tail) > b.size {
		copy(b.tail, b.tail[len(b.tail)-b.size:])
		b.tail = b.tail[:b.size]
	}
	subs := append([]chan Event(nil), b.subs...)
	b.mu.Unlock()
	for _, sub := range subs {
		select {
		case sub <- e:
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (b *EventBus) Tail() []Event {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]Event(nil), b.tail...)
}

func (b *EventBus) subscriberCount() int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func RunJSONLSubscriber(ctx context.Context, bus *EventBus, sink runmetrics.Sink) {
	if bus == nil || sink == nil {
		return
	}
	ch, cancel := bus.Subscribe(64)
	defer cancel()
	defer sink.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			_ = sink.Emit(ctx, e)
		}
	}
}
