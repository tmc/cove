package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestExtractFleetRunAll(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantAll bool
		want    []string
	}{
		{name: "no flag", args: []string{"--", "echo"}, wantAll: false, want: []string{"--", "echo"}},
		{name: "double dash all", args: []string{"--all", "--", "echo"}, wantAll: true, want: []string{"--", "echo"}},
		{name: "single dash all", args: []string{"-all", "x"}, wantAll: true, want: []string{"x"}},
		{name: "all amid args", args: []string{"a", "--all", "b"}, wantAll: true, want: []string{"a", "b"}},
		{name: "empty", args: nil, wantAll: false, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAll, got := extractFleetRunAll(tt.args)
			if gotAll != tt.wantAll {
				t.Errorf("all = %v, want %v", gotAll, tt.wantAll)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("args = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRunFleetFanOutAllSuccess(t *testing.T) {
	path := writeFleetHostsConfig(t, "a", "b")
	runner := &fakeFleetRunner{outputs: map[string]string{
		"a.local": "started\n",
		"b.local": "started\n",
	}}
	var out, errOut bytes.Buffer
	if err := runFleetRunCommand(context.Background(), []string{"--all"}, path, runner, &out, &errOut); err != nil {
		t.Fatalf("runFleetRunCommand: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "a\tok") || !strings.Contains(got, "b\tok") {
		t.Errorf("output missing ok rows:\n%s", got)
	}
	if !strings.Contains(got, "fan-out: 2 ok, 0 failed") {
		t.Errorf("output missing summary:\n%s", got)
	}
	runner.assertSawCall(t, "a.local", []string{"run"})
	runner.assertSawCall(t, "b.local", []string{"run"})
}

func TestRunFleetFanOutPolicyFanOut(t *testing.T) {
	path := writeFleetHostsConfig(t, "a")
	runner := &fakeFleetRunner{outputs: map[string]string{"a.local": "ok\n"}}
	var out bytes.Buffer
	if err := runFleetRunCommand(context.Background(), []string{"--policy=fan-out", "--", "echo"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("runFleetRunCommand: %v", err)
	}
	runner.assertSawCall(t, "a.local", []string{"run", "--", "echo"})
}

func TestRunFleetFanOutPartialFailure(t *testing.T) {
	path := writeFleetHostsConfig(t, "a", "b", "c")
	runner := &fakeFleetRunner{
		outputs: map[string]string{"a.local": "ok\n", "c.local": "ok\n"},
		errs:    map[string]error{"b.local": errors.New("exit status 1")},
	}
	var out, errOut bytes.Buffer
	err := runFleetRunCommand(context.Background(), []string{"--all"}, path, runner, &out, &errOut)
	if err == nil {
		t.Fatal("want error when a host fails")
	}
	if !strings.Contains(err.Error(), "1 of 3 hosts failed") {
		t.Errorf("err = %v, want '1 of 3 hosts failed'", err)
	}
	got := out.String()
	if !strings.Contains(got, "b\t(error)") {
		t.Errorf("output missing error row for b:\n%s", got)
	}
	if !strings.Contains(got, "fan-out: 2 ok, 1 failed") {
		t.Errorf("output missing summary:\n%s", got)
	}
}

func TestRunFleetFanOutAllUnreachable(t *testing.T) {
	path := writeFleetHostsConfig(t, "a", "b")
	runner := &fakeFleetRunner{errs: map[string]error{
		"a.local": errors.New("connection refused"),
		"b.local": errors.New("connection refused"),
	}}
	var out, errOut bytes.Buffer
	err := runFleetRunCommand(context.Background(), []string{"--all"}, path, runner, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "2 of 2 hosts failed") {
		t.Fatalf("err = %v, want '2 of 2 hosts failed'", err)
	}
	got := out.String()
	if !strings.Contains(got, "a\t(error)") || !strings.Contains(got, "b\t(error)") {
		t.Errorf("output missing error rows:\n%s", got)
	}
	if !strings.Contains(got, "fan-out: 0 ok, 2 failed") {
		t.Errorf("output missing summary:\n%s", got)
	}
}

func TestRunFleetFanOutNoRemotes(t *testing.T) {
	path := writeFleetHostsConfig(t)
	var out bytes.Buffer
	if err := runFleetRunCommand(context.Background(), []string{"--all"}, path, &fakeFleetRunner{}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("runFleetRunCommand: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "no fleet remotes" {
		t.Errorf("got %q, want 'no fleet remotes'", got)
	}
}
