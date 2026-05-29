//go:build integration && darwin && arm64

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func testNetwork(t *testing.T, vm *testVM) {
	t.Run("guest-curl", func(t *testing.T) {
		out := strings.TrimSpace(agentExec(t, vm,
			"/usr/bin/curl",
			"-fsSL",
			"-o", "/dev/null",
			"-w", "%{http_code}",
			"https://example.com",
		))
		if out != "200" {
			t.Fatalf("guest curl: got %q, want %q", out, "200")
		}
	})

	t.Run("proxy-macos", func(t *testing.T) {
		requireGUI(t)
		cloneName := integrationCloneName(t.Name())
		if err := CloneVM(CloneOptions{Source: vm.name, Target: cloneName, Linked: true}); err != nil {
			t.Fatalf("CloneVM() error = %v", err)
		}
		clone := clonedTestVM(t, cloneName, false)

		startTestVMWithArgs(t, clone, "-proxy", "http://192.168.64.1:8080")
		waitVMReadyTB(t, clone, integrationVMReadyTimeout(clone, false))
		requireUserAgent(t, clone)

		service := firstMacOSNetworkService(t, clone)
		waitForMacOSProxyState(t, clone, service, true, "192.168.64.1", 8080)

		clone.cleanupTB(t)
		startTestVM(t, clone)
		waitVMReadyTB(t, clone, integrationVMReadyTimeout(clone, false))
		requireUserAgent(t, clone)
		waitForMacOSProxyState(t, clone, service, false, "", 0)
	})
}

func testPortForward(t *testing.T, vm *testVM) {
	t.Run("lifecycle", func(t *testing.T) {
		hostPort := pickFreeTCPPort(t)
		const guestPort = uint32(9999)

		ctlDo(t, vm, portForwardRequest("start", hostPort, guestPort))
		t.Cleanup(func() {
			req := portForwardRequest("stop", hostPort, 0)
			req.AuthToken = vm.token
			_, _ = ctlSendRequest(vm.sock, req, 10*time.Second, req.Type)
		})

		list := responseMessage(ctlDo(t, vm, portForwardRequest("list", 0, 0)))
		want := fmt.Sprintf("localhost:%d -> vsock:%d", hostPort, guestPort)
		if !strings.Contains(list, want) {
			t.Fatalf("port-forward list: got %q, want entry containing %q", list, want)
		}

		ctlDo(t, vm, portForwardRequest("stop", hostPort, 0))

		list = responseMessage(ctlDo(t, vm, portForwardRequest("list", 0, 0)))
		if strings.Contains(list, fmt.Sprintf("localhost:%d", hostPort)) {
			t.Fatalf("port-forward stop: stale entry still present in %q", list)
		}
	})
}

func firstMacOSNetworkService(t *testing.T, vm *testVM) string {
	t.Helper()
	out := userAgentExec(t, vm, "/usr/sbin/networksetup", "-listallnetworkservices")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "An asterisk") || strings.HasPrefix(line, "*") {
			continue
		}
		return line
	}
	t.Fatal("no enabled macOS network service found")
	return ""
}

func waitForMacOSProxyState(t *testing.T, vm *testVM, service string, enabled bool, server string, port int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		web := parseMacOSProxyStatusTB(t, userAgentExec(t, vm, "/usr/sbin/networksetup", "-getwebproxy", service))
		secure := parseMacOSProxyStatusTB(t, userAgentExec(t, vm, "/usr/sbin/networksetup", "-getsecurewebproxy", service))
		if proxyStatusMatches(web, enabled, server, port) && proxyStatusMatches(secure, enabled, server, port) {
			return
		}
		time.Sleep(2 * time.Second)
	}

	web := parseMacOSProxyStatusTB(t, userAgentExec(t, vm, "/usr/sbin/networksetup", "-getwebproxy", service))
	secure := parseMacOSProxyStatusTB(t, userAgentExec(t, vm, "/usr/sbin/networksetup", "-getsecurewebproxy", service))
	t.Fatalf("proxy state for %s did not converge: web=%+v secure=%+v", service, web, secure)
}

func parseMacOSProxyStatusTB(t *testing.T, out string) proxyServiceStatus {
	t.Helper()
	status, err := parseNetworkSetupProxyStatus(out)
	if err != nil {
		t.Fatalf("parseNetworkSetupProxyStatus() = %v\noutput:\n%s", err, out)
	}
	return status
}

func proxyStatusMatches(status proxyServiceStatus, enabled bool, server string, port int) bool {
	if status.Enabled != enabled {
		return false
	}
	if !enabled {
		return true
	}
	return status.Server == server && status.Port == port
}
