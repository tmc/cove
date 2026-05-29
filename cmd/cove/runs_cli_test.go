package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/metrics"
	"github.com/tmc/cove/internal/runs"
)

func TestParseRunsShowArgsAllowsFlagsAfterPrefix(t *testing.T) {
	prefix, jsonOut, err := parseRunsShowArgs([]string{"abc123", "--json"})
	if err != nil {
		t.Fatalf("parseRunsShowArgs: %v", err)
	}
	if prefix != "abc123" || !jsonOut {
		t.Fatalf("prefix/json = %q/%v, want abc123/true", prefix, jsonOut)
	}
}

func TestRunsUsageDocumentsNDJSONAlias(t *testing.T) {
	var b bytes.Buffer
	printRunsUsage(&b)
	if !strings.Contains(b.String(), "--json|--ndjson") {
		t.Fatalf("usage = %q, want ndjson alias", b.String())
	}
}

func TestRunsListHelpExitsZero(t *testing.T) {
	if err := runRunsList(commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)}, []string{"-h"}); err != nil {
		t.Fatalf("runRunsList(-h): %v", err)
	}
	var b bytes.Buffer
	printRunsListUsage(&b)
	for _, want := range []string{"Usage: cove runs list", "--status ok|fail|all", "--json", "--ndjson"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("usage missing %q:\n%s", want, b.String())
		}
	}
}

func TestRunsShowExportHelpExitsZero(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"show", []string{"show", "--help"}, "Usage: cove runs show"},
		{"export", []string{"export", "--help"}, "Usage: cove runs export"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := handleRunsCommand(commandEnv{Stdout: &out, Stderr: new(bytes.Buffer)}, tt.args); err != nil {
				t.Fatalf("handleRunsCommand(%v): %v", tt.args, err)
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("help output = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestRunRunsListJSONModes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".vz", "runs")
	writeRunsCLIRun(t, root, "20260510-a", "vm-a")
	writeRunsCLIRun(t, root, "20260510-b", "vm-b")

	var jsonBuf bytes.Buffer
	if err := runRunsList(commandEnv{Stdout: &jsonBuf, Stderr: new(bytes.Buffer)}, []string{"--json", "--limit", "2"}); err != nil {
		t.Fatalf("runRunsList --json: %v", err)
	}
	jsonOut := jsonBuf.String()
	var summaries []runs.Summary
	if err := json.Unmarshal([]byte(jsonOut), &summaries); err != nil {
		t.Fatalf("--json output is not a JSON array: %v\n%s", err, jsonOut)
	}
	if len(summaries) != 2 {
		t.Fatalf("--json summaries = %d, want 2: %s", len(summaries), jsonOut)
	}

	var ndjsonBuf bytes.Buffer
	if err := runRunsList(commandEnv{Stdout: &ndjsonBuf, Stderr: new(bytes.Buffer)}, []string{"--ndjson", "--limit", "2"}); err != nil {
		t.Fatalf("runRunsList --ndjson: %v", err)
	}
	ndjsonOut := ndjsonBuf.String()
	if got := strings.Count(strings.TrimSpace(ndjsonOut), "\n") + 1; got != 2 {
		t.Fatalf("--ndjson lines = %d, want 2:\n%s", got, ndjsonOut)
	}
}

func TestRunRunsListLimitZeroReturnsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".vz", "runs")
	writeRunsCLIRun(t, root, "20260510-a", "vm-a")

	var out bytes.Buffer
	if err := runRunsList(commandEnv{Stdout: &out, Stderr: new(bytes.Buffer)}, []string{"--json", "--limit", "0"}); err != nil {
		t.Fatalf("runRunsList --limit 0: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("--limit 0 JSON = %q, want []", strings.TrimSpace(out.String()))
	}
}

func TestRunRunsListJSONMissingRootReturnsEmptyArray(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out bytes.Buffer
	env := commandEnv{Stdout: &out, Stderr: new(bytes.Buffer)}
	if err := runRunsList(env, []string{"--json"}); err != nil {
		t.Fatalf("runRunsList --json missing root: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("missing root JSON = %q, want []", strings.TrimSpace(out.String()))
	}

	out.Reset()
	if err := runRunsList(env, []string{"--ndjson"}); err != nil {
		t.Fatalf("runRunsList --ndjson missing root: %v", err)
	}
	if out.String() != "" {
		t.Fatalf("missing root NDJSON = %q, want empty output", out.String())
	}
}

func TestRunRunsShowMissingJSONWritesMachineReadableStdout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	err := runRunsShow(commandEnv{Stdout: &out, Stderr: new(bytes.Buffer)}, []string{"missing", "--json"})
	if err == nil {
		t.Fatal("runRunsShow --json missing = nil, want error")
	}
	if strings.Contains(out.String(), "error:") {
		t.Fatalf("stdout contains plain text diagnostic: %q", out.String())
	}
	var got runsShowErrorOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if got.RunID != "missing" || got.Error == "" {
		t.Fatalf("JSON output = %#v, want run_id/error", got)
	}
}

func TestParseRunsExportArgsAllowsFormatAfterPrefix(t *testing.T) {
	tests := [][]string{
		{"abc123", "--format", "gha-summary"},
		{"abc123", "--format=gha-summary"},
		{"--format", "gha-summary", "abc123"},
	}
	for _, args := range tests {
		prefix, format, guestPaths, err := parseRunsExportArgs(args)
		if err != nil {
			t.Fatalf("parseRunsExportArgs(%v): %v", args, err)
		}
		if prefix != "abc123" || format != "gha-summary" || len(guestPaths) != 0 {
			t.Fatalf("parseRunsExportArgs(%v) = %q, %q, %v; want abc123, gha-summary, nil", args, prefix, format, guestPaths)
		}
	}
}

func TestParseRunsExportArgsIncludesGuest(t *testing.T) {
	prefix, format, guestPaths, err := parseRunsExportArgs([]string{"abc123", "--format=tar", "--include-guest", "/tmp/out.txt", "--include-guest=/var/log/app.log"})
	if err != nil {
		t.Fatalf("parseRunsExportArgs: %v", err)
	}
	if prefix != "abc123" || format != "tar" {
		t.Fatalf("prefix/format = %q/%q, want abc123/tar", prefix, format)
	}
	if got, want := strings.Join(guestPaths, ","), "/tmp/out.txt,/var/log/app.log"; got != want {
		t.Fatalf("guest paths = %q, want %q", got, want)
	}
}

func TestParseRunsExportArgsErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing format value", []string{"abc", "--format"}, "requires a value"},
		{"unknown flag", []string{"abc", "--bogus"}, "unknown runs export flag"},
		{"two prefixes", []string{"abc", "def", "--format=json"}, "usage:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := parseRunsExportArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v; want contains %q", err, tt.want)
			}
		})
	}
}

func TestRunRunsExportIncludeGuestCopiesIntoTar(t *testing.T) {
	root := t.TempDir()
	writeRunsCLIRun(t, root, "20260510-guest", "job-vm")
	fake := newFakeCpAgent()
	fake.guest["/tmp/report.txt"] = []byte("guest report\n")
	var buf bytes.Buffer
	if err := runRunsExportWith(context.Background(), []string{"20260510", "--format=tar", "--include-guest", "/tmp/report.txt"}, root, &buf, func(vm string) cpAgent {
		if vm != "job-vm" {
			t.Fatalf("vm = %q, want job-vm", vm)
		}
		return fake
	}); err != nil {
		t.Fatalf("runRunsExportWith: %v", err)
	}
	names := runsTarNames(t, buf.Bytes())
	if !names["20260510-guest/guest/tmp/report.txt"] {
		t.Fatalf("tar missing guest artifact: %#v", names)
	}
}

func TestRunRunsExportIncludeGuestFailures(t *testing.T) {
	root := t.TempDir()
	writeRunsCLIRun(t, root, "20260510-guest", "job-vm")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"non tar", []string{"20260510", "--format=json", "--include-guest", "/tmp/a"}, "requires --format tar"},
		{"relative guest", []string{"20260510", "--format=tar", "--include-guest", "tmp/a"}, "must be absolute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := runRunsExportWith(context.Background(), tt.args, root, &buf, func(string) cpAgent {
				t.Fatal("agent should not be constructed")
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunRunsExportRejectsUnknownFormat(t *testing.T) {
	err := runRunsExport(commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)}, []string{"abc123", "--format=yaml"})
	if err == nil || !strings.Contains(err.Error(), "unknown runs export format") {
		t.Fatalf("err = %v; want unknown format error", err)
	}
}

func TestRunRunsExportRequiresPrefixAndFormat(t *testing.T) {
	tests := [][]string{
		{},
		{"--format=json"},
		{"abc123"},
	}
	for _, args := range tests {
		err := runRunsExport(commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)}, args)
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("runRunsExport(%v) err = %v; want usage error", args, err)
		}
	}
}

func TestPrintRunsTableIncludesEventCount(t *testing.T) {
	exit := 0
	summaries := []runs.Summary{{
		RunID:           "20260510-abcdef",
		ImageRef:        "ubuntu:24.04",
		VMName:          "vm1",
		Status:          "ok",
		TotalDurationMS: 250,
		EventCount:      7,
		ExitCode:        &exit,
		StartedAt:       time.Unix(0, 0).UTC(),
	}}
	var buf bytes.Buffer
	if err := printRunsTable(&buf, summaries); err != nil {
		t.Fatalf("printRunsTable: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "EVENTS") {
		t.Fatalf("missing EVENTS header:\n%s", out)
	}
	if !strings.Contains(out, " 7 ") {
		t.Fatalf("missing event count cell '7':\n%s", out)
	}
}

func writeRunsCLIRun(t *testing.T, root, id, vm string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	f, err := os.Create(filepath.Join(dir, "metrics.jsonl"))
	if err != nil {
		t.Fatalf("Create metrics: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, event := range []metrics.Event{
		{Timestamp: time.Unix(0, 0).UTC().Format(time.RFC3339Nano), EventType: "vm_start", VMName: vm, Status: "ok"},
		{Timestamp: time.Unix(1, 0).UTC().Format(time.RFC3339Nano), EventType: "run_complete", VMName: vm, Status: "ok", Extra: map[string]any{"run_id": id}},
	} {
		if err := enc.Encode(event); err != nil {
			t.Fatalf("Encode metrics: %v", err)
		}
	}
}

func runsTarNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		names[h.Name] = true
	}
	return names
}
