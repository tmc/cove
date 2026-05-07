package main

import (
	"testing"

	"github.com/tmc/vz-macos/internal/controlserver"
)

func TestParseLinuxLoginctlSessionsActiveWayland(t *testing.T) {
	in := []byte(`[
		{"session":"1","uid":1000,"user":"desk","seat":"seat0","state":"active","type":"wayland"},
		{"session":"2","uid":120,"user":"gdm","seat":"seat0","state":"online","type":"wayland"}
	]`)
	got, ok, err := parseLinuxLoginctlSessions(in)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no session found")
	}
	want := controlserver.GUISession{ID: "1", User: "desk", Seat: "seat0", Kind: "wayland"}
	if got != want {
		t.Fatalf("session = %#v, want %#v", got, want)
	}
}

func TestParseLinuxLoginctlSessionsActiveX11NameFallback(t *testing.T) {
	in := []byte(`[
		{"session":"3","uid":1000,"name":"qa","seat":"seat0","state":"active","type":"x11"}
	]`)
	got, ok, err := parseLinuxLoginctlSessions(in)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no session found")
	}
	want := controlserver.GUISession{ID: "3", User: "qa", Seat: "seat0", Kind: "x11"}
	if got != want {
		t.Fatalf("session = %#v, want %#v", got, want)
	}
}

func TestParseLinuxLoginctlSessionsSkipsNonGraphical(t *testing.T) {
	in := []byte(`[
		{"session":"1","uid":1000,"user":"desk","seat":"seat0","state":"online","type":"wayland"},
		{"session":"2","uid":1000,"user":"desk","seat":"seat0","state":"active","type":"tty"}
	]`)
	_, ok, err := parseLinuxLoginctlSessions(in)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("found non-graphical session")
	}
}

func TestParseLinuxLoginctlSessionsRejectsMalformedJSON(t *testing.T) {
	if _, _, err := parseLinuxLoginctlSessions([]byte(`not-json`)); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseLoginctlShowGUISession(t *testing.T) {
	got, ok := parseLoginctlShowGUISession("1", "Name=desk\nUser=1000\nSeat=seat0\nState=active\nType=wayland\n")
	if !ok {
		t.Fatal("no session found")
	}
	want := controlserver.GUISession{ID: "1", User: "desk", Seat: "seat0", Kind: "wayland"}
	if got != want {
		t.Fatalf("session = %#v, want %#v", got, want)
	}
}
