package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForceBootCommandAutomationBackends(t *testing.T) {
	tests := []struct {
		name         string
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

func TestForceBootCommandAutomationBackendsNil(t *testing.T) {
	// nil ControlServer must yield a no-op restore func, not panic.
	restore := forceBootCommandAutomationBackends(nil)
	if restore == nil {
		t.Fatal("restore func is nil")
	}
	restore()
}

func TestRunAutomationScript(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		file    string
		write   string // empty => don't create
		wantErr string
	}{
		{
			name:    "missing file",
			file:    filepath.Join(dir, "missing.vzscript"),
			wantErr: "read automation script",
		},
		{
			name:    "legacy angle-bracket format rejected",
			file:    filepath.Join(dir, "legacy.txt"),
			write:   "<wait>5s</wait>\n<type>hello</type>\n",
			wantErr: "unsupported automation format",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.write != "" {
				if err := os.WriteFile(tt.file, []byte(tt.write), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err := runAutomationScript(&ControlServer{}, "", tt.file)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}
