package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseForwardSpec(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vm      string
		mapping string
		want    forwardSpec
		err     string
	}{
		{
			name:    "valid",
			vm:      "vm1",
			mapping: "8080:80",
			want: forwardSpec{
				VM:        "vm1",
				HostPort:  8080,
				GuestPort: 80,
				RelayPort: uint32(forwardRelayBasePort + 8080%forwardRelayPortWindow),
				AgentPort: uint32(forwardRelayBasePort + 8080%forwardRelayPortWindow + forwardAgentPortOffset),
			},
		},
		{name: "empty vm", vm: "", mapping: "8080:80", err: "vm required"},
		{name: "slash vm", vm: "bad/vm", mapping: "8080:80", err: "invalid VM name"},
		{name: "missing colon", vm: "vm1", mapping: "8080", err: "expected hostport:vmport"},
		{name: "empty host", vm: "vm1", mapping: ":80", err: "expected hostport:vmport"},
		{name: "extra colon", vm: "vm1", mapping: "8080:80:1", err: "expected hostport:vmport"},
		{name: "zero host", vm: "vm1", mapping: "0:80", err: "invalid host port"},
		{name: "zero guest", vm: "vm1", mapping: "8080:0", err: "invalid vm port"},
		{name: "large host", vm: "vm1", mapping: "65536:80", err: "invalid host port"},
		{name: "large guest", vm: "vm1", mapping: "8080:65536", err: "invalid vm port"},
		{name: "host name", vm: "vm1", mapping: "host:80", err: "invalid host port"},
		{name: "guest name", vm: "vm1", mapping: "8080:http", err: "invalid vm port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseForwardSpec(tc.vm, tc.mapping)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("parseForwardSpec error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseForwardSpec: %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseForwardSpec = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRunForwardUsesStarter(t *testing.T) {
	starter := &fakeForwardStarter{}
	newStarter := func(vm string) forwardStarter {
		if vm != "vm1" {
			t.Fatalf("starter vm = %q, want vm1", vm)
		}
		return starter
	}
	if err := runForward(context.Background(), []string{"vm1", "8080:80"}, newStarter); err != nil {
		t.Fatalf("runForward: %v", err)
	}
	if starter.spec.VM != "vm1" || starter.spec.HostPort != 8080 || starter.spec.GuestPort != 80 {
		t.Fatalf("starter spec = %#v, want vm1 8080:80", starter.spec)
	}
	if starter.spec.RelayPort == 0 || starter.spec.AgentPort == 0 || starter.spec.RelayPort == starter.spec.AgentPort {
		t.Fatalf("starter relay ports not assigned: %#v", starter.spec)
	}
}

func TestRunForwardPropagatesStarterError(t *testing.T) {
	want := errors.New("boom")
	newStarter := func(vm string) forwardStarter {
		return &fakeForwardStarter{err: want}
	}
	err := runForward(context.Background(), []string{"vm1", "8080:80"}, newStarter)
	if !errors.Is(err, want) {
		t.Fatalf("runForward error = %v, want %v", err, want)
	}
}

type fakeForwardStarter struct {
	spec forwardSpec
	err  error
}

func (f *fakeForwardStarter) StartForward(ctx context.Context, spec forwardSpec) (string, error) {
	f.spec = spec
	if f.err != nil {
		return "", f.err
	}
	return "forward started", nil
}
