package main

import (
	"context"
	"testing"
)

func TestApplyMacOSProxyExistingServices(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	state := &macOSProxyState{
		Services: []macOSProxyServiceState{
			{Name: "Wi-Fi"},
			{Name: "Ethernet"},
		},
	}
	spec := proxySpec{Host: "127.0.0.1", Port: 8080}

	got, err := applyMacOSProxy(context.Background(), rt, state, spec)
	if err != nil {
		t.Fatalf("applyMacOSProxy() error = %v", err)
	}
	if got != state {
		t.Fatalf("got = %p, want %p (passthrough)", got, state)
	}
	for _, svc := range []string{"Wi-Fi", "Ethernet"} {
		if !rt.hasUserCall(commandKey([]string{"/usr/sbin/networksetup", "-setwebproxy", svc, "127.0.0.1", "8080", "on"})) {
			t.Fatalf("missing -setwebproxy call for %s", svc)
		}
	}
}

func TestApplyMacOSProxyNilStateInitializes(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	rt.setUserExecResponse([]string{"/usr/sbin/networksetup", "-listallnetworkservices"}, "An asterisk...\nWi-Fi\n")
	rt.setUserExecResponse([]string{"/usr/sbin/networksetup", "-getwebproxy", "Wi-Fi"}, "Enabled: No\nServer:\nPort: 0\nAuthenticated Proxy Enabled: 0\n")
	rt.setUserExecResponse([]string{"/usr/sbin/networksetup", "-getsecurewebproxy", "Wi-Fi"}, "Enabled: No\nServer:\nPort: 0\nAuthenticated Proxy Enabled: 0\n")
	spec := proxySpec{Host: "127.0.0.1", Port: 8080}

	got, err := applyMacOSProxy(context.Background(), rt, nil, spec)
	if err != nil {
		t.Fatalf("applyMacOSProxy(nil) error = %v", err)
	}
	if got == nil || len(got.Services) != 1 || got.Services[0].Name != "Wi-Fi" {
		t.Fatalf("got = %#v, want one service Wi-Fi", got)
	}
}
