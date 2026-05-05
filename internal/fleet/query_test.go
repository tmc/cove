package fleet

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueryAllMixedSuccessFailure(t *testing.T) {
	entries := []Entry{
		{Name: "a", Remote: Remote{Host: "a.local"}},
		{Name: "b", Remote: Remote{Host: "b.local"}},
		{Name: "c", Remote: Remote{Host: "c.local"}},
	}
	boom := errors.New("boom")
	got := QueryAll(context.Background(), entries, func(ctx context.Context, e Entry) (string, error) {
		if e.Name == "b" {
			return "", boom
		}
		return "ok-" + e.Name, nil
	})
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Host != "a" || got[1].Host != "b" || got[2].Host != "c" {
		t.Fatalf("order = %#v", got)
	}
	if got[0].Value != "ok-a" || got[0].Error != nil {
		t.Fatalf("a = %#v", got[0])
	}
	if !errors.Is(got[1].Error, boom) {
		t.Fatalf("b error = %v, want boom", got[1].Error)
	}
	if got[2].Value != "ok-c" || got[2].Error != nil {
		t.Fatalf("c = %#v", got[2])
	}
}

func TestQueryAllTimeout(t *testing.T) {
	entries := []Entry{{Name: "fast"}, {Name: "slow"}}
	start := time.Now()
	got := QueryAllWithTimeout(context.Background(), entries, 20*time.Millisecond, func(ctx context.Context, e Entry) (string, error) {
		if e.Name == "fast" {
			return "ok", nil
		}
		<-ctx.Done()
		return "", ctx.Err()
	})
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("QueryAllWithTimeout took %s, want prompt timeout", elapsed)
	}
	if got[0].Value != "ok" || got[0].Error != nil {
		t.Fatalf("fast = %#v", got[0])
	}
	if !errors.Is(got[1].Error, context.DeadlineExceeded) {
		t.Fatalf("slow error = %v, want deadline exceeded", got[1].Error)
	}
}

func TestQueryAllRunsInParallel(t *testing.T) {
	entries := []Entry{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	started := make(chan string, len(entries))
	release := make(chan struct{})
	done := make(chan []FleetResult[string], 1)
	go func() {
		done <- QueryAll(context.Background(), entries, func(ctx context.Context, e Entry) (string, error) {
			started <- e.Name
			<-release
			return e.Name, nil
		})
	}()
	seen := map[string]bool{}
	for range entries {
		seen[<-started] = true
	}
	if len(seen) != 3 {
		t.Fatalf("started = %#v, want all entries", seen)
	}
	close(release)
	got := <-done
	for i, e := range entries {
		if got[i].Value != e.Name {
			t.Fatalf("result[%d] = %#v, want %s", i, got[i], e.Name)
		}
	}
}

func TestQueryAllParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := QueryAll(ctx, []Entry{{Name: "a"}, {Name: "b"}}, func(ctx context.Context, e Entry) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	for _, r := range got {
		if !errors.Is(r.Error, context.Canceled) {
			t.Fatalf("%s error = %v, want canceled", r.Host, r.Error)
		}
	}
}
