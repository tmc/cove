package agent

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestNewOneShotConnDialerNilConn(t *testing.T) {
	if _, _, err := newOneShotConnDialer(nil); err == nil || !strings.Contains(err.Error(), "nil conn") {
		t.Fatalf("err = %v, want nil-conn error", err)
	}
}

func TestNewOneShotConnDialerSingleUse(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	dial, closeFn, err := newOneShotConnDialer(client)
	if err != nil {
		t.Fatalf("newOneShotConnDialer: %v", err)
	}
	defer closeFn()

	got, err := dial(context.Background())
	if err != nil {
		t.Fatalf("dial1: %v", err)
	}
	if got != client {
		t.Fatalf("dial returned different conn")
	}
	if _, err := dial(context.Background()); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("dial2 err = %v, want already-used", err)
	}
}

func TestNewOneShotConnDialerCloseBeforeUse(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	_, closeFn, err := newOneShotConnDialer(client)
	if err != nil {
		t.Fatalf("newOneShotConnDialer: %v", err)
	}
	closeFn()
	// Underlying conn should now be closed: writes fail.
	if _, err := client.Write([]byte("x")); err == nil {
		t.Fatalf("expected write on closed conn to fail")
	}
}

func TestNewOneShotConnDialerCtxCanceled(t *testing.T) {
	_, client := net.Pipe()
	defer client.Close()

	dial, closeFn, err := newOneShotConnDialer(client)
	if err != nil {
		t.Fatalf("newOneShotConnDialer: %v", err)
	}
	defer closeFn()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dial(ctx); err == nil {
		t.Fatalf("expected canceled ctx to fail dial")
	}
}

func TestNewOneShotConnDialerPostCloseDial(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	dial, closeFn, err := newOneShotConnDialer(client)
	if err != nil {
		t.Fatalf("newOneShotConnDialer: %v", err)
	}
	if _, err := dial(context.Background()); err != nil {
		t.Fatalf("dial: %v", err)
	}
	closeFn()
	if _, err := dial(context.Background()); err == nil || !strings.Contains(err.Error(), "client closed") {
		t.Fatalf("post-close dial err = %v, want client closed", err)
	}
}
