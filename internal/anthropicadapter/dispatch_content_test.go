package anthropicadapter

import (
	"context"
	"strings"
	"testing"
)

func TestDispatchContentRejectsUnknownTool(t *testing.T) {
	a := &Adapter{Control: &fakeControl{}}
	_, err := a.dispatchContent(context.Background(), Block{Name: "other"})
	if err == nil || !strings.Contains(err.Error(), "unsupported tool") {
		t.Fatalf("err = %v, want unsupported tool", err)
	}
}

func TestDispatchContentTypeAction(t *testing.T) {
	a := &Adapter{Control: &fakeControl{}}
	got, err := a.dispatchContent(context.Background(), Block{
		Name:  "computer",
		Input: map[string]any{"action": "type", "text": "hi"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "typed" {
		t.Fatalf("got %v, want typed", got)
	}
}

func TestDispatchContentScrollFallsBackToScrollAmount(t *testing.T) {
	a := &Adapter{Control: &fakeControl{}}
	got, err := a.dispatchContent(context.Background(), Block{
		Name:  "computer",
		Input: map[string]any{"action": "scroll", "scroll_amount": float64(5)},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "scrolled" {
		t.Fatalf("got %v, want scrolled", got)
	}
}

func TestDispatchContentCursorPosition(t *testing.T) {
	a := &Adapter{Control: &fakeControl{}}
	got, err := a.dispatchContent(context.Background(), Block{
		Name:  "computer",
		Input: map[string]any{"action": "cursor_position"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	s, _ := got.(string)
	if !strings.HasPrefix(s, "cursor_position: ") {
		t.Fatalf("got %q, want cursor_position prefix", s)
	}
}

func TestDispatchContentRejectsUnknownAction(t *testing.T) {
	a := &Adapter{Control: &fakeControl{}}
	_, err := a.dispatchContent(context.Background(), Block{
		Name:  "computer",
		Input: map[string]any{"action": "no-such"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported computer action") {
		t.Fatalf("err = %v", err)
	}
}
