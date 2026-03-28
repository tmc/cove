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
