package softreset

import (
	"context"
	"errors"
	"testing"
)

func TestProcessTableProbePassesWhenCoveProcessesAreGone(t *testing.T) {
	snapshots := [][]Process{
		{
			{PID: 1, UID: 0, Cmdline: "launchd"},
			{PID: 20, UID: 501, Cmdline: "cove-softreset-probe worker"},
		},
		{
			{PID: 1, UID: 0, Cmdline: "launchd"},
			{PID: 42, UID: 0, Cmdline: "syslogd"},
		},
	}
	got, err := (ProcessTableProbe{Snapshot: fakeSnapshots(snapshots)}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusPass {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "cove-spawned=empty-after-reset") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestProcessTableProbeFailsWhenCoveProcessesSurvive(t *testing.T) {
	snapshots := [][]Process{
		{
			{PID: 1, UID: 0, Cmdline: "launchd"},
			{PID: 20, UID: 501, Cmdline: "cove-softreset-probe worker"},
		},
		{
			{PID: 1, UID: 0, Cmdline: "launchd"},
			{PID: 20, UID: 501, Cmdline: "cove-softreset-probe worker"},
		},
	}
	got, err := (ProcessTableProbe{Snapshot: fakeSnapshots(snapshots)}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusFail {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "cove-spawned=survived") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestProcessTableProbeLimitsWhenNotArmed(t *testing.T) {
	got, err := (ProcessTableProbe{Snapshot: fakeSnapshots([][]Process{{
		{PID: 1, UID: 0, Cmdline: "launchd"},
	}})}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusLimit {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "cove-spawned=not-observed") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestProcessTableProbeReportsResetError(t *testing.T) {
	want := errors.New("reset failed")
	_, err := (ProcessTableProbe{
		Snapshot: fakeSnapshots([][]Process{{
			{PID: 1, UID: 0, Cmdline: "launchd"},
			{PID: 2, UID: 501, Cmdline: "cove-softreset-probe worker"},
		}}),
		Reset: func(context.Context) error { return want },
	}).Run(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestParsePSLine(t *testing.T) {
	p, ok := parsePSLine("  123  501 /usr/bin/cove-softreset-probe worker")
	if !ok {
		t.Fatal("parsePSLine failed")
	}
	if p.PID != 123 || p.UID != 501 || p.Cmdline != "/usr/bin/cove-softreset-probe worker" {
		t.Fatalf("process = %+v", p)
	}
}

func fakeSnapshots(snapshots [][]Process) func(context.Context) ([]Process, error) {
	var n int
	return func(context.Context) ([]Process, error) {
		if n >= len(snapshots) {
			return snapshots[len(snapshots)-1], nil
		}
		out := snapshots[n]
		n++
		return out, nil
	}
}
