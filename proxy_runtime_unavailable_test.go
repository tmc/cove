package main

import (
	"context"
	"errors"
	"testing"
)

func TestProxyRuntimeUnavailableSentinel(t *testing.T) {
	r := &proxyRuntimeClient{}
	ctx := context.Background()

	if _, err := r.Exec(ctx, []string{"echo"}, nil, ""); !errors.Is(err, ErrProxyRuntimeUnavailable) {
		t.Fatalf("Exec err = %v, want ErrProxyRuntimeUnavailable", err)
	}
	if _, err := r.UserExec(ctx, []string{"echo"}, nil, ""); !errors.Is(err, ErrProxyRuntimeUnavailable) {
		t.Fatalf("UserExec err = %v, want ErrProxyRuntimeUnavailable", err)
	}
	if _, err := r.ReadFile(ctx, "/etc/hosts"); !errors.Is(err, ErrProxyRuntimeUnavailable) {
		t.Fatalf("ReadFile err = %v, want ErrProxyRuntimeUnavailable", err)
	}
	if err := r.WriteFile(ctx, "/etc/hosts", nil, 0644); !errors.Is(err, ErrProxyRuntimeUnavailable) {
		t.Fatalf("WriteFile err = %v, want ErrProxyRuntimeUnavailable", err)
	}
}

func TestProxyRuntimeUnavailableHelpers(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "configure guest proxy without control server",
			run:  func() error { return configureGuestProxy(ctx, nil, "http://127.0.0.1:8080") },
		},
		{
			name: "wait without control server",
			run:  func() error { return waitForProxyRuntime(ctx, nil) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, ErrProxyRuntimeUnavailable) {
				t.Fatalf("err = %v, want ErrProxyRuntimeUnavailable", err)
			}
		})
	}
}
