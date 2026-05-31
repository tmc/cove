package fleet

import (
	"context"
	"errors"
	"testing"
)

func TestSelectLeastLoadedHost(t *testing.T) {
	entries := []Entry{
		{Name: "b", Remote: Remote{Host: "b.local"}},
		{Name: "a", Remote: Remote{Host: "a.local"}},
		{Name: "c", Remote: Remote{Host: "c.local"}},
	}
	outputs := map[string]string{
		"a": "vm1 running\nvm2 stopped\n",
		"b": "vm1 running\nvm2 running\n",
		"c": "",
	}
	got, loads, err := SelectLeastLoadedHost(context.Background(), entries, func(ctx context.Context, e Entry) (string, error) {
		return outputs[e.Name], nil
	})
	if err != nil {
		t.Fatalf("SelectLeastLoadedHost: %v", err)
	}
	if got.Name != "c" {
		t.Fatalf("selected %q, want c", got.Name)
	}
	if len(loads) != 3 || loads[0].Count != 2 || loads[1].Count != 1 || loads[2].Count != 0 {
		t.Fatalf("loads = %#v", loads)
	}
}

func TestSelectLeastLoadedHostTieBreaksAlphabetically(t *testing.T) {
	entries := []Entry{
		{Name: "b", Remote: Remote{Host: "b.local"}},
		{Name: "a", Remote: Remote{Host: "a.local"}},
	}
	got, _, err := SelectLeastLoadedHost(context.Background(), entries, func(ctx context.Context, e Entry) (string, error) {
		return "vm running\n", nil
	})
	if err != nil {
		t.Fatalf("SelectLeastLoadedHost: %v", err)
	}
	if got.Name != "a" {
		t.Fatalf("selected %q, want a", got.Name)
	}
}

func TestSelectLeastLoadedHostIgnoresUnreachable(t *testing.T) {
	entries := []Entry{{Name: "a"}, {Name: "b"}}
	got, loads, err := SelectLeastLoadedHost(context.Background(), entries, func(ctx context.Context, e Entry) (string, error) {
		if e.Name == "a" {
			return "", errors.New("offline")
		}
		return "vm running\n", nil
	})
	if err != nil {
		t.Fatalf("SelectLeastLoadedHost: %v", err)
	}
	if got.Name != "b" {
		t.Fatalf("selected %q, want b", got.Name)
	}
	if loads[0].Error == nil {
		t.Fatalf("loads = %#v, want first host error", loads)
	}
}

func TestSelectLeastLoadedHostSkipsCordoned(t *testing.T) {
	entries := []Entry{
		{Name: "a", Remote: Remote{Host: "a.local", Cordoned: true}},
		{Name: "b", Remote: Remote{Host: "b.local"}},
	}
	called := map[string]bool{}
	got, loads, err := SelectLeastLoadedHost(context.Background(), entries, func(ctx context.Context, e Entry) (string, error) {
		called[e.Name] = true
		return "", nil
	})
	if err != nil {
		t.Fatalf("SelectLeastLoadedHost: %v", err)
	}
	if got.Name != "b" {
		t.Fatalf("selected %q, want b", got.Name)
	}
	if called["a"] {
		t.Fatalf("queried cordoned host: %#v", called)
	}
	if len(loads) != 2 || !loads[0].Cordoned || loads[1].Host != "b" {
		t.Fatalf("loads = %#v, want cordoned a then b", loads)
	}
	if got := FormatHostLoads(loads); got != "a=cordoned b=0" {
		t.Fatalf("FormatHostLoads = %q, want cordoned summary", got)
	}
}

func TestSelectLeastLoadedHostAllCordoned(t *testing.T) {
	_, loads, err := SelectLeastLoadedHost(context.Background(), []Entry{{Name: "a", Remote: Remote{Cordoned: true}}}, func(ctx context.Context, e Entry) (string, error) {
		t.Fatal("queried cordoned host")
		return "", nil
	})
	if err == nil || err.Error() != "fleet placement: all remotes cordoned" {
		t.Fatalf("err = %v, want all remotes cordoned", err)
	}
	if len(loads) != 1 || !loads[0].Cordoned {
		t.Fatalf("loads = %#v, want cordoned load", loads)
	}
}

func TestSelectLeastLoadedHostAllUnreachable(t *testing.T) {
	_, _, err := SelectLeastLoadedHost(context.Background(), []Entry{{Name: "a"}}, func(ctx context.Context, e Entry) (string, error) {
		return "", errors.New("offline")
	})
	if err == nil {
		t.Fatal("SelectLeastLoadedHost succeeded, want error")
	}
}

func TestCountRunningVMs(t *testing.T) {
	got := CountRunningVMs("one running\ntwo stopped\nthree Running\n")
	if got != 2 {
		t.Fatalf("CountRunningVMs = %d, want 2", got)
	}
}
