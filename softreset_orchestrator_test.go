package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/softreset"
)

func TestParseSoftresetRunAllArgs(t *testing.T) {
	got, err := parseSoftresetRunAllArgs([]string{"test-vm", "--filter=mem,fs,net,proc", "--timeout=5s", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if got.VM != "test-vm" || !got.JSON || got.Timeout.String() != "5s" {
		t.Fatalf("options = %+v", got)
	}
	if strings.Join(got.Filter, ",") != "filesystem,process,network,memory" {
		t.Fatalf("Filter = %v", got.Filter)
	}
}

func TestSoftResetRunAllReport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	vmDir := filepath.Join(home, ".vz", "vms", "test-vm")
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		t.Fatal(err)
	}

	old := softresetRunProbe
	t.Cleanup(func() { softresetRunProbe = old })
	var order []string
	softresetRunProbe = func(_ context.Context, name, _ string) (softreset.Result, error) {
		order = append(order, name)
		switch name {
		case "filesystem":
			return softreset.Result{Probe: "filesystem-attributes", Status: softreset.StatusPass, Evidence: []string{"sentinel=absent-after-reset"}}, nil
		case "process":
			return softreset.Result{Probe: "process-table", Status: softreset.StatusLimit, Evidence: []string{"cove-spawned=not-observed"}}, nil
		case "network":
			return softreset.Result{}, errors.New("network reset failed")
		case "memory":
			return softreset.Result{Probe: "memory", Status: softreset.StatusPass, Evidence: []string{"markers=absent-after-reset"}}, nil
		default:
			t.Fatalf("unexpected probe %q", name)
			return softreset.Result{}, nil
		}
	}

	report, err := SoftResetRunAll(context.Background(), "test-vm", softresetRunAllOptions{
		Filter:  []string{"memory", "network", "filesystem", "process"},
		Timeout: 30_000_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "filesystem,process,network,memory" {
		t.Fatalf("order = %v", order)
	}
	if report.IsolationScore != 63 {
		t.Fatalf("IsolationScore = %d, want 63", report.IsolationScore)
	}
	if len(report.Probes) != 4 {
		t.Fatalf("len(Probes) = %d", len(report.Probes))
	}
	if report.Probes[2].Status != "fail" || report.Probes[2].Error == "" {
		t.Fatalf("network report = %+v", report.Probes[2])
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"isolation_score":63`) {
		t.Fatalf("json missing score: %s", data)
	}
}

func TestSoftResetRunAllTimeout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	vmDir := filepath.Join(home, ".vz", "vms", "test-vm")
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		t.Fatal(err)
	}

	old := softresetRunProbe
	t.Cleanup(func() { softresetRunProbe = old })
	softresetRunProbe = func(ctx context.Context, name, _ string) (softreset.Result, error) {
		<-ctx.Done()
		return softreset.Result{}, ctx.Err()
	}

	report, err := SoftResetRunAll(context.Background(), "test-vm", softresetRunAllOptions{
		Filter:  []string{"filesystem"},
		Timeout: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Probes) != 1 || report.Probes[0].Status != "timeout" {
		t.Fatalf("timeout report = %+v", report.Probes)
	}
}
