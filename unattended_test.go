package main

import "testing"

func TestForceBootCommandAutomationBackends(t *testing.T) {
	tests := []struct {
		name        string
		startCapture automationBackendMode
		startInput   automationBackendMode
		during       automationBackendMode
		afterCapture automationBackendMode
		afterInput   automationBackendMode
	}{
		{
			name:         "auto becomes framebuffer temporarily",
			startCapture: automationBackendAuto,
			startInput:   automationBackendAuto,
			during:       automationBackendFramebuffer,
			afterCapture: automationBackendAuto,
			afterInput:   automationBackendAuto,
		},
		{
			name:         "window becomes framebuffer temporarily",
			startCapture: automationBackendWindow,
			startInput:   automationBackendWindow,
			during:       automationBackendFramebuffer,
			afterCapture: automationBackendWindow,
			afterInput:   automationBackendWindow,
		},
		{
			name:         "framebuffer remains framebuffer",
			startCapture: automationBackendFramebuffer,
			startInput:   automationBackendFramebuffer,
			during:       automationBackendFramebuffer,
			afterCapture: automationBackendFramebuffer,
			afterInput:   automationBackendFramebuffer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &ControlServer{}
			cs.setCaptureBackend(tt.startCapture)
			cs.setInputBackend(tt.startInput)

			restore := forceBootCommandAutomationBackends(cs)
			if got := cs.captureBackend(); got != tt.during {
				t.Fatalf("capture backend during boot commands = %v, want %v", got, tt.during)
			}
			if got := cs.inputBackend(); got != tt.during {
				t.Fatalf("input backend during boot commands = %v, want %v", got, tt.during)
			}

			restore()
			if got := cs.captureBackend(); got != tt.afterCapture {
				t.Fatalf("capture backend after restore = %v, want %v", got, tt.afterCapture)
			}
			if got := cs.inputBackend(); got != tt.afterInput {
				t.Fatalf("input backend after restore = %v, want %v", got, tt.afterInput)
			}
		})
	}
}
