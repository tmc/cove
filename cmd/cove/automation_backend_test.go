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

func TestAutomationBackendModeStrings(t *testing.T) {
	tests := []struct {
		mode                        automationBackendMode
		str, input, keyboard, mouse string
	}{
		{automationBackendAuto, "auto", "auto", "auto", "auto"},
		{automationBackendFramebuffer, "framebuffer", "direct", "direct", "direct"},
		{automationBackendWindow, "window", "window", "cgevent", "cgevent"},
	}
	for _, tt := range tests {
		t.Run(tt.str, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.str {
				t.Errorf("String() = %q, want %q", got, tt.str)
			}
			if got := tt.mode.inputString(); got != tt.input {
				t.Errorf("inputString() = %q, want %q", got, tt.input)
			}
			if got := tt.mode.keyboardMode(); got != tt.keyboard {
				t.Errorf("keyboardMode() = %q, want %q", got, tt.keyboard)
			}
			if got := tt.mode.mouseMode(); got != tt.mouse {
				t.Errorf("mouseMode() = %q, want %q", got, tt.mouse)
			}
		})
	}
}

func TestCombinedAutomationBackend(t *testing.T) {
	tests := []struct {
		name           string
		capture, input automationBackendMode
		want           string
	}{
		{"both auto", automationBackendAuto, automationBackendAuto, "auto"},
		{"both framebuffer", automationBackendFramebuffer, automationBackendFramebuffer, "framebuffer"},
		{"both window", automationBackendWindow, automationBackendWindow, "window"},
		{"mixed auto+window", automationBackendAuto, automationBackendWindow, "mixed"},
		{"mixed framebuffer+window", automationBackendFramebuffer, automationBackendWindow, "mixed"},
		{"mixed window+framebuffer", automationBackendWindow, automationBackendFramebuffer, "mixed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := combinedAutomationBackend(tt.capture, tt.input); got != tt.want {
				t.Errorf("combinedAutomationBackend(%v,%v) = %q, want %q", tt.capture, tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveAutomationBackends(t *testing.T) {
	saveB := automationBackend
	saveC := automationCaptureBackend
	saveI := automationInputBackend
	t.Cleanup(func() {
		automationBackend = saveB
		automationCaptureBackend = saveC
		automationInputBackend = saveI
	})

	tests := []struct {
		name        string
		combined    string
		captureFlag string
		inputFlag   string
		wantCapture automationBackendMode
		wantInput   automationBackendMode
	}{
		{"all empty defaults to auto", "", "", "", automationBackendAuto, automationBackendAuto},
		{"invalid combined falls back to auto", "bogus", "", "", automationBackendAuto, automationBackendAuto},
		{"combined=window", "window", "", "", automationBackendWindow, automationBackendWindow},
		{"capture overrides combined", "window", "framebuffer", "", automationBackendFramebuffer, automationBackendWindow},
		{"input overrides combined", "framebuffer", "", "window", automationBackendFramebuffer, automationBackendWindow},
		{"invalid capture override is ignored", "window", "bogus", "", automationBackendWindow, automationBackendWindow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			automationBackend = tc.combined
			automationCaptureBackend = tc.captureFlag
			automationInputBackend = tc.inputFlag
			capture, input := resolveAutomationBackends()
			if capture != tc.wantCapture || input != tc.wantInput {
				t.Fatalf("capture=%v input=%v, want capture=%v input=%v", capture, input, tc.wantCapture, tc.wantInput)
			}
		})
	}
}
