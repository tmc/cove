package main

import (
	"context"
	"strings"
	"testing"
)

func TestRestoreMacOSProxyServiceEnabledMissingState(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	state := macOSProxyServiceState{
		Name:       "Wi-Fi",
		WebEnabled: true, // server/port intentionally zero
	}
	err := restoreMacOSProxyService(context.Background(), rt, state)
	if err == nil || !strings.Contains(err.Error(), "missing state") {
		t.Fatalf("err = %v, want 'missing state'", err)
	}
}

func TestRestoreMacOSProxyServiceDisabledTurnsOff(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	state := macOSProxyServiceState{Name: "Wi-Fi"}
	if err := restoreMacOSProxyService(context.Background(), rt, state); err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, kind := range []string{"-setwebproxystate", "-setsecurewebproxystate"} {
		if !rt.hasUserCall(commandKey([]string{"/usr/sbin/networksetup", kind, "Wi-Fi", "off"})) {
			t.Fatalf("missing %s off call", kind)
		}
	}
}

func TestRestoreMacOSProxyServiceEnabledRestoresAndTurnsOn(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	state := macOSProxyServiceState{
		Name:          "Wi-Fi",
		WebEnabled:    true,
		WebServer:     "10.0.0.1",
		WebPort:       3128,
		SecureEnabled: false,
	}
	if err := restoreMacOSProxyService(context.Background(), rt, state); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !rt.hasUserCall(commandKey([]string{"/usr/sbin/networksetup", "-setwebproxy", "Wi-Fi", "10.0.0.1", "3128", "on"})) {
		t.Fatal("missing -setwebproxy call")
	}
	if !rt.hasUserCall(commandKey([]string{"/usr/sbin/networksetup", "-setwebproxystate", "Wi-Fi", "on"})) {
		t.Fatal("missing -setwebproxystate on")
	}
}
