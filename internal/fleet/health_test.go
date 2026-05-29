package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClassifyProbe(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		err        error
		wantStatus HostStatus
		wantDetail string
	}{
		{name: "online", output: "vm-a running", wantStatus: StatusOnline},
		{name: "online ignores detail", output: "no vms", wantStatus: StatusOnline},
		{name: "degraded empty", output: "", wantStatus: StatusDegraded, wantDetail: "empty probe response"},
		{name: "degraded whitespace", output: "   \n\t", wantStatus: StatusDegraded, wantDetail: "empty probe response"},
		{name: "unreachable", output: "", err: errors.New("dial: refused"), wantStatus: StatusUnreachable, wantDetail: "dial: refused"},
		{name: "error wins over output", output: "stale", err: errors.New("timeout"), wantStatus: StatusUnreachable, wantDetail: "timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, detail := ClassifyProbe(tt.output, tt.err)
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
			if tt.wantDetail != "" && detail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", detail, tt.wantDetail)
			}
		})
	}
}

func TestProbeHostsFailSoft(t *testing.T) {
	entries := []Entry{
		{Name: "c", Remote: Remote{Host: "c"}},
		{Name: "a", Remote: Remote{Host: "a"}},
		{Name: "b", Remote: Remote{Host: "b"}},
	}
	health := ProbeHosts(context.Background(), entries, time.Second, func(ctx context.Context, e Entry) (string, error) {
		switch e.Name {
		case "a":
			return "vm-1 running", nil
		case "b":
			return "", nil // reachable but empty -> degraded
		default:
			return "", errors.New("connection refused")
		}
	})
	if len(health) != 3 {
		t.Fatalf("got %d rows, want 3", len(health))
	}
	// Sorted by host name.
	if health[0].Host != "a" || health[1].Host != "b" || health[2].Host != "c" {
		t.Fatalf("rows not sorted by host: %#v", health)
	}
	if health[0].Status != StatusOnline {
		t.Errorf("a status = %q, want online", health[0].Status)
	}
	if health[1].Status != StatusDegraded {
		t.Errorf("b status = %q, want degraded", health[1].Status)
	}
	if health[2].Status != StatusUnreachable {
		t.Errorf("c status = %q, want unreachable", health[2].Status)
	}
	summary := SummarizeHealth(health)
	if summary.Online != 1 || summary.Degraded != 1 || summary.Unreachable != 1 {
		t.Errorf("summary = %+v, want 1/1/1", summary)
	}
}

func TestProbeHostsAllUnreachable(t *testing.T) {
	entries := []Entry{{Name: "x", Remote: Remote{Host: "x"}}, {Name: "y", Remote: Remote{Host: "y"}}}
	health := ProbeHosts(context.Background(), entries, 0, func(ctx context.Context, e Entry) (string, error) {
		return "", errors.New("no route to host")
	})
	for _, h := range health {
		if h.Status != StatusUnreachable {
			t.Errorf("%s status = %q, want unreachable", h.Host, h.Status)
		}
	}
	if s := SummarizeHealth(health); s.Unreachable != 2 {
		t.Errorf("unreachable = %d, want 2", s.Unreachable)
	}
}

func TestFormatHostHealth(t *testing.T) {
	tests := []struct {
		name   string
		health []HostHealth
		want   string
	}{
		{name: "empty", health: nil, want: "no fleet remotes\n"},
		{name: "online no detail", health: []HostHealth{{Host: "a", Status: StatusOnline}}, want: "a\tonline\n"},
		{
			name: "mixed",
			health: []HostHealth{
				{Host: "a", Status: StatusOnline},
				{Host: "b", Status: StatusDegraded, Detail: "empty probe response"},
				{Host: "c", Status: StatusUnreachable, Detail: "dial: refused"},
			},
			want: "a\tonline\nb\tdegraded\tempty probe response\nc\tunreachable\tdial: refused\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatHostHealth(tt.health); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProbeHostsParallel(t *testing.T) {
	// Each probe sleeps; total wall time must be well under the serial sum,
	// proving the probes fan out concurrently.
	entries := make([]Entry, 8)
	for i := range entries {
		entries[i] = Entry{Name: string(rune('a' + i)), Remote: Remote{Host: string(rune('a' + i))}}
	}
	start := time.Now()
	ProbeHosts(context.Background(), entries, time.Second, func(ctx context.Context, e Entry) (string, error) {
		time.Sleep(80 * time.Millisecond)
		return "ok", nil
	})
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("probes not concurrent: elapsed %v", elapsed)
	}
}

func TestSummarizeHealthEmpty(t *testing.T) {
	if s := SummarizeHealth(nil); s != (HealthSummary{}) {
		t.Fatalf("got %+v, want zero summary", s)
	}
}

func TestHostStatusStrings(t *testing.T) {
	// Guard against accidental renames that JSON consumers depend on.
	if !strings.EqualFold(string(StatusOnline), "online") ||
		!strings.EqualFold(string(StatusDegraded), "degraded") ||
		!strings.EqualFold(string(StatusUnreachable), "unreachable") {
		t.Fatal("HostStatus constant values changed")
	}
}
