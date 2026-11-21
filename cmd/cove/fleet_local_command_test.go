package main

import (
	"bytes"
	"strings"
	"testing"

	fleetpkg "github.com/tmc/cove/internal/fleet"
)

func TestIsLocalFleetRemote(t *testing.T) {
	tests := []struct {
		name   string
		remote fleetpkg.Remote
		want   bool
	}{
		{name: "empty host is local", remote: fleetpkg.Remote{}, want: true},
		{name: "user-only is local", remote: fleetpkg.Remote{User: "alice"}, want: true},
		{name: "with host is remote", remote: fleetpkg.Remote{Host: "vm.example"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLocalFleetRemote(tt.remote); got != tt.want {
				t.Fatalf("isLocalFleetRemote(%+v) = %v, want %v", tt.remote, got, tt.want)
			}
		})
	}
}

func TestRunLocalCoveCommandUnsupported(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no args", args: nil, want: "fleet local command unsupported"},
		{name: "non-image", args: []string{"vm", "list"}, want: "fleet local command unsupported"},
		{name: "image push wrong arity", args: []string{"image", "push", "foo"}, want: "fleet local image push unsupported"},
		{name: "image push not stdin", args: []string{"image", "push", "foo:tag", "/tmp/x"}, want: "fleet local image push unsupported"},
		{name: "image load wrong arity", args: []string{"image", "load"}, want: "fleet local image load unsupported"},
		{name: "image load not stdin", args: []string{"image", "load", "/tmp/x"}, want: "fleet local image load unsupported"},
		{name: "image other verb", args: []string{"image", "rm", "foo"}, want: "fleet local image command unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runLocalCoveCommand(tt.args, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil {
				t.Fatalf("runLocalCoveCommand(%v) = nil, want error containing %q", tt.args, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("runLocalCoveCommand(%v) err = %v, want contains %q", tt.args, err, tt.want)
			}
		})
	}
}

func TestRunLocalCoveCommandPushBadRef(t *testing.T) {
	err := runLocalCoveCommand([]string{"image", "push", "BAD/REF/WITH/SLASHES", "-"}, nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected ParseImageRef error, got nil")
	}
}

func TestGatewayFallbackPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := gatewayFallbackPath()
	if err != nil {
		t.Fatalf("gatewayFallbackPath: %v", err)
	}
	if !strings.HasSuffix(got, "/.vz/gateway.token") {
		t.Fatalf("path = %q, want suffix /.vz/gateway.token", got)
	}
}
