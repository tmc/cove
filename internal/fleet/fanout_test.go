package fleet

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFanOutAllSuccess(t *testing.T) {
	entries := []Entry{
		{Name: "b", Remote: Remote{Host: "b"}},
		{Name: "a", Remote: Remote{Host: "a"}},
	}
	res := FanOut(context.Background(), entries, time.Second, func(ctx context.Context, e Entry) (string, error) {
		return "started " + e.Name + "\n", nil
	})
	if res.Success != 2 || res.Failed != 0 {
		t.Fatalf("success=%d failed=%d, want 2/0", res.Success, res.Failed)
	}
	if len(res.Outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(res.Outcomes))
	}
	// Sorted by host.
	if res.Outcomes[0].Host != "a" || res.Outcomes[1].Host != "b" {
		t.Fatalf("outcomes not sorted: %#v", res.Outcomes)
	}
	for _, o := range res.Outcomes {
		if !o.OK {
			t.Errorf("%s OK = false, want true", o.Host)
		}
		if o.Output != "started "+o.Host { // trailing newline trimmed
			t.Errorf("%s output = %q", o.Host, o.Output)
		}
	}
}

func TestFanOutPartialFailure(t *testing.T) {
	entries := []Entry{
		{Name: "a", Remote: Remote{Host: "a"}},
		{Name: "b", Remote: Remote{Host: "b"}},
		{Name: "c", Remote: Remote{Host: "c"}},
	}
	res := FanOut(context.Background(), entries, time.Second, func(ctx context.Context, e Entry) (string, error) {
		if e.Name == "b" {
			return "", errors.New("disk full")
		}
		return "ok", nil
	})
	if res.Success != 2 || res.Failed != 1 {
		t.Fatalf("success=%d failed=%d, want 2/1", res.Success, res.Failed)
	}
	for _, o := range res.Outcomes {
		if o.Host == "b" {
			if o.OK || o.Error != "disk full" {
				t.Errorf("b outcome = %#v, want failed with disk full", o)
			}
		}
	}
}

func TestFanOutAllUnreachable(t *testing.T) {
	entries := []Entry{{Name: "a", Remote: Remote{Host: "a"}}, {Name: "b", Remote: Remote{Host: "b"}}}
	res := FanOut(context.Background(), entries, 0, func(ctx context.Context, e Entry) (string, error) {
		return "", errors.New("connection refused")
	})
	if res.Success != 0 || res.Failed != 2 {
		t.Fatalf("success=%d failed=%d, want 0/2", res.Success, res.Failed)
	}
	for _, o := range res.Outcomes {
		if o.OK {
			t.Errorf("%s OK = true, want false", o.Host)
		}
	}
}

func TestFanOutEmpty(t *testing.T) {
	res := FanOut(context.Background(), nil, time.Second, func(ctx context.Context, e Entry) (string, error) {
		return "", nil
	})
	if res.Success != 0 || res.Failed != 0 || len(res.Outcomes) != 0 {
		t.Fatalf("got %+v, want empty result", res)
	}
}
