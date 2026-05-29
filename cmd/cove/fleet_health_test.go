package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	fleetpkg "github.com/tmc/cove/internal/fleet"
)

func TestRunFleetHealthCommandMixed(t *testing.T) {
	path := writeFleetHostsConfig(t, "a", "b", "c")
	runner := &fakeFleetRunner{
		outputs: map[string]string{
			"a.local": "vm-1\trunning",
			"b.local": "", // reachable but empty -> degraded
		},
		errs: map[string]error{
			"c.local": errors.New("connection refused"),
		},
	}
	var out, errOut bytes.Buffer
	if err := runFleetHealthCommand(context.Background(), nil, path, runner, &out, &errOut); err != nil {
		t.Fatalf("runFleetHealthCommand: %v", err)
	}
	got := out.String()
	for _, want := range []string{"a\tonline", "b\tdegraded", "c\tunreachable", "1 online, 1 degraded, 1 unreachable"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRunFleetHealthCommandJSON(t *testing.T) {
	path := writeFleetHostsConfig(t, "a", "b")
	runner := &fakeFleetRunner{
		outputs: map[string]string{"a.local": "vm running"},
		errs:    map[string]error{"b.local": errors.New("timeout")},
	}
	var out bytes.Buffer
	if err := runFleetHealthCommand(context.Background(), []string{"--json"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("runFleetHealthCommand: %v", err)
	}
	var health []fleetpkg.HostHealth
	if err := json.Unmarshal(out.Bytes(), &health); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if len(health) != 2 {
		t.Fatalf("got %d hosts, want 2", len(health))
	}
	byHost := map[string]fleetpkg.HostStatus{}
	for _, h := range health {
		byHost[h.Host] = h.Status
	}
	if byHost["a"] != fleetpkg.StatusOnline {
		t.Errorf("a status = %q, want online", byHost["a"])
	}
	if byHost["b"] != fleetpkg.StatusUnreachable {
		t.Errorf("b status = %q, want unreachable", byHost["b"])
	}
}

func TestRunFleetHealthCommandNoRemotes(t *testing.T) {
	path := writeFleetHostsConfig(t)
	var out bytes.Buffer
	if err := runFleetHealthCommand(context.Background(), nil, path, &fakeFleetRunner{}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("runFleetHealthCommand: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "no fleet remotes" {
		t.Errorf("got %q, want 'no fleet remotes'", got)
	}
}

func TestRunFleetHealthCommandNoRemotesJSON(t *testing.T) {
	path := writeFleetHostsConfig(t)
	var out bytes.Buffer
	if err := runFleetHealthCommand(context.Background(), []string{"--json"}, path, &fakeFleetRunner{}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("runFleetHealthCommand: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "[]" {
		t.Errorf("got %q, want '[]'", got)
	}
}
