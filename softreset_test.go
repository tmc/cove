package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/softreset"
)

func TestParseSoftresetProbeArgsDefaultAll(t *testing.T) {
	got, err := parseSoftresetProbeArgs([]string{"test-vm"})
	if err != nil {
		t.Fatal(err)
	}
	if got.VM != "test-vm" || !got.All {
		t.Fatalf("options = %+v", got)
	}
	if strings.Join(got.Probes, ",") != "filesystem,process,network,memory" {
		t.Fatalf("Probes = %v", got.Probes)
	}
}

func TestParseSoftresetProbeArgsSubsetAfterVM(t *testing.T) {
	got, err := parseSoftresetProbeArgs([]string{"test-vm", "--probes", "network,memory"})
	if err != nil {
		t.Fatal(err)
	}
	if got.All {
		t.Fatalf("All = true, want false")
	}
	if strings.Join(got.Probes, ",") != "network,memory" {
		t.Fatalf("Probes = %v", got.Probes)
	}
}

func TestParseSoftresetProbeArgsRejectsUnknown(t *testing.T) {
	_, err := parseSoftresetProbeArgs([]string{"test-vm", "--probes", "network,unknown"})
	if err == nil {
		t.Fatal("parse accepted unknown probe")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error = %v", err)
	}
}

func TestWriteSoftresetProbeSummary(t *testing.T) {
	var b bytes.Buffer
	err := writeSoftresetProbeSummary(&b, softresetProbeOptions{VM: "test-vm"}, []softreset.Result{
		{Probe: "filesystem-attributes", Status: softreset.StatusPass, Evidence: []string{"sentinel=absent-after-reset"}},
		{Probe: "process-table", Status: softreset.StatusLimit, Evidence: []string{"cove-spawned=not-observed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"Soft reset probe summary for test-vm", "filesystem-attributes", "Pass: 1", "Limit: 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestSoftresetNoArgsShowsUsage(t *testing.T) {
	err := softresetCommand(nil)
	if err == nil || !strings.Contains(err.Error(), "command required") {
		t.Fatalf("err = %v, want command required", err)
	}
}

func TestSoftresetHelpUsage(t *testing.T) {
	var b strings.Builder
	printSoftresetUsage(&b)
	for _, want := range []string{"Usage: cove softreset", "probe", "run-all"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("usage missing %q:\n%s", want, b.String())
		}
	}
}
