package main

import (
	"context"
	"testing"
	"time"
)

func TestControlServerLifecycleContextShutdown(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())

	ctx := s.lifecycleContext()
	if ctx == nil {
		t.Fatal("lifecycleContext() = nil")
	}
	select {
	case <-ctx.Done():
		t.Fatal("lifecycle context already canceled")
	default:
	}

	timeoutCtx, cancel := s.timeoutContext(time.Hour)
	defer cancel()
	select {
	case <-timeoutCtx.Done():
		t.Fatal("timeout context canceled early")
	default:
	}

	s.shutdownLifecycleContext()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel lifecycle context")
	}
	select {
	case <-timeoutCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel derived timeout context")
	}

	s.shutdownLifecycleContext()
	if got := s.lifecycleContext(); got != context.Background() {
		t.Fatalf("lifecycleContext after shutdown = %v, want Background", got)
	}
}
