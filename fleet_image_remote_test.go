package main

import (
	"strings"
	"testing"

	fleetpkg "github.com/tmc/vz-macos/internal/fleet"
)

func TestFleetRemoteByNameMissing(t *testing.T) {
	cfg := &fleetpkg.Config{}
	_, err := fleetRemoteByName(cfg, "nope")
	if err == nil {
		t.Fatal("fleetRemoteByName(empty cfg) = nil, want not-found")
	}
	if !strings.Contains(err.Error(), `remote "nope" not found`) {
		t.Fatalf("err = %v, want 'remote \"nope\" not found'", err)
	}
}

func TestFleetRemoteByNameReturnsKnown(t *testing.T) {
	want := fleetpkg.Remote{Host: "host.example", User: "alice", DefaultVM: "dev"}
	cfg := &fleetpkg.Config{Remotes: map[string]fleetpkg.Remote{"lab": want}}
	got, err := fleetRemoteByName(cfg, "lab")
	if err != nil {
		t.Fatalf("fleetRemoteByName: %v", err)
	}
	if got.Host != want.Host || got.User != want.User || got.DefaultVM != want.DefaultVM {
		t.Fatalf("got = %#v, want %#v", got, want)
	}
}
