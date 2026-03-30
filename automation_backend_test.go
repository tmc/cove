package main

import "testing"

func TestParseAutomationBackend(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    automationBackendMode
		wantErr bool
	}{
		{name: "default", input: "", want: automationBackendAuto},
		{name: "auto", input: "auto", want: automationBackendAuto},
		{name: "framebuffer", input: "framebuffer", want: automationBackendFramebuffer},
		{name: "window", input: "window", want: automationBackendWindow},
		{name: "trim and fold", input: "  WiNdOw  ", want: automationBackendWindow},
		{name: "invalid", input: "ax", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAutomationBackend(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAutomationBackend(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAutomationBackend(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("parseAutomationBackend(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseAutomationInputBackend(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    automationBackendMode
		wantErr bool
	}{
		{name: "default", input: "", want: automationBackendAuto},
		{name: "direct", input: "direct", want: automationBackendFramebuffer},
		{name: "framebuffer alias", input: "framebuffer", want: automationBackendFramebuffer},
		{name: "window", input: "window", want: automationBackendWindow},
		{name: "invalid", input: "ax", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAutomationInputBackend(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseAutomationInputBackend(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAutomationInputBackend(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("parseAutomationInputBackend(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestAutomationBackendCaptureMode(t *testing.T) {
	tests := []struct {
		name   string
		mode   automationBackendMode
		headed bool
		want   string
	}{
		{name: "auto headed", mode: automationBackendAuto, headed: true, want: "window"},
		{name: "auto headless", mode: automationBackendAuto, headed: false, want: "private-framebuffer"},
		{name: "framebuffer headed", mode: automationBackendFramebuffer, headed: true, want: "private-framebuffer"},
		{name: "window headless", mode: automationBackendWindow, headed: false, want: "window"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mode.captureMode(tt.headed); got != tt.want {
				t.Fatalf("captureMode(%v, headed=%v) = %q, want %q", tt.mode, tt.headed, got, tt.want)
			}
		})
	}
}
