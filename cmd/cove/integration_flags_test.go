//go:build integration && darwin && arm64

package main

import "testing"

func TestIntegrationHeadlessMode(t *testing.T) {
	prevHeadless := *flagIntegrationHeadless
	prevHeaded := *flagIntegrationHeaded
	t.Cleanup(func() {
		*flagIntegrationHeadless = prevHeadless
		*flagIntegrationHeaded = prevHeaded
	})

	tests := []struct {
		name     string
		linux    bool
		headless bool
		headed   bool
		want     bool
	}{
		{name: "linux always headless", linux: true, headed: true, want: true},
		{name: "mac defaults to headed", want: false},
		{name: "mac headless flag", headless: true, want: true},
		{name: "headed overrides headless", headless: true, headed: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*flagIntegrationHeadless = tt.headless
			*flagIntegrationHeaded = tt.headed
			if got := integrationHeadlessMode(tt.linux); got != tt.want {
				t.Fatalf("integrationHeadlessMode(%t) = %t, want %t", tt.linux, got, tt.want)
			}
		})
	}
}
