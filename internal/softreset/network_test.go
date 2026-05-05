package softreset

import (
	"context"
	"errors"
	"testing"
)

func TestNetworkProbePassesWhenCoveSocketsAreGone(t *testing.T) {
	snapshots := [][]NetworkSocket{
		{
			{Protocol: "tcp", Local: "127.0.0.1:22", State: "LISTEN", Process: "sshd"},
			{Protocol: "tcp", Local: "127.0.0.1:49152", Remote: "127.0.0.1:80", State: "ESTAB", Process: "cove-softreset-probe"},
		},
		{
			{Protocol: "tcp", Local: "127.0.0.1:22", State: "LISTEN", Process: "sshd"},
		},
	}
	got, err := (NetworkProbe{Snapshot: fakeNetworkSnapshots(snapshots)}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusPass {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "cove-sockets=empty-after-reset") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestNetworkProbeFailsWhenCoveSocketsSurvive(t *testing.T) {
	snapshots := [][]NetworkSocket{
		{
			{Protocol: "tcp", Local: "127.0.0.1:22", State: "LISTEN", Process: "sshd"},
			{Protocol: "tcp", Local: "127.0.0.1:49152", Remote: "127.0.0.1:80", State: "ESTAB", Process: "cove-softreset-probe"},
		},
		{
			{Protocol: "tcp", Local: "127.0.0.1:22", State: "LISTEN", Process: "sshd"},
			{Protocol: "tcp", Local: "127.0.0.1:49152", Remote: "127.0.0.1:80", State: "ESTAB", Process: "cove-softreset-probe"},
		},
	}
	got, err := (NetworkProbe{Snapshot: fakeNetworkSnapshots(snapshots)}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusFail {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "cove-sockets=survived") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestNetworkProbeLimitsWhenNotArmed(t *testing.T) {
	got, err := (NetworkProbe{Snapshot: fakeNetworkSnapshots([][]NetworkSocket{{
		{Protocol: "tcp", Local: "127.0.0.1:22", State: "LISTEN", Process: "sshd"},
	}})}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusLimit {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "cove-sockets=not-observed") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestNetworkProbeReportsResetError(t *testing.T) {
	want := errors.New("reset failed")
	_, err := (NetworkProbe{
		Snapshot: fakeNetworkSnapshots([][]NetworkSocket{{
			{Protocol: "tcp", Local: "127.0.0.1:22", State: "LISTEN", Process: "sshd"},
			{Protocol: "tcp", Local: "127.0.0.1:49152", Remote: "127.0.0.1:80", State: "ESTAB", Process: "cove-softreset-probe"},
		}}),
		Reset: func(context.Context) error { return want },
	}).Run(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func fakeNetworkSnapshots(snapshots [][]NetworkSocket) func(context.Context) ([]NetworkSocket, error) {
	var n int
	return func(context.Context) ([]NetworkSocket, error) {
		if n >= len(snapshots) {
			return snapshots[len(snapshots)-1], nil
		}
		out := snapshots[n]
		n++
		return out, nil
	}
}
