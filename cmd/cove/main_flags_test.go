package main

import (
	"strings"
	"testing"
)

func TestValidateTopLevelProvisionStrategy(t *testing.T) {
	old := provisionStrategy
	t.Cleanup(func() { provisionStrategy = old })

	tests := []struct {
		name      string
		strategy  string
		wantValue string
		wantErr   string
	}{
		{name: "disk", strategy: "disk", wantValue: "disk"},
		{name: "gui", strategy: "gui", wantValue: "gui"},
		{name: "auto", strategy: "auto", wantValue: "auto"},
		{name: "inject alias", strategy: "inject", wantValue: "disk"},
		{name: "invalid", strategy: "manual", wantErr: "invalid -provision-strategy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provisionStrategy = tt.strategy
			err := validateLaunchOptions()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateLaunchOptions() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateLaunchOptions() error = %v", err)
			}
			if provisionStrategy != tt.wantValue {
				t.Fatalf("provisionStrategy = %q, want %q", provisionStrategy, tt.wantValue)
			}
		})
	}
}

func TestValidateTopLevelLaunchOrder(t *testing.T) {
	old := launchOrder
	t.Cleanup(func() { launchOrder = old })

	tests := []struct {
		name    string
		order   string
		wantErr string
	}{
		{name: "window first", order: "window-first"},
		{name: "start first", order: "start-first"},
		{name: "invalid", order: "after-window", wantErr: "invalid -launch-order"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			launchOrder = tt.order
			err := validateLaunchOptions()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateLaunchOptions() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateLaunchOptions() error = %v", err)
			}
		})
	}
}
