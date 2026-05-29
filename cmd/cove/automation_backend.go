package main

import (
	"fmt"
	"strings"
)

type automationBackendMode int32

const (
	automationBackendAuto automationBackendMode = iota
	automationBackendFramebuffer
	automationBackendWindow
)

func parseAutomationBackend(s string) (automationBackendMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return automationBackendAuto, nil
	case "framebuffer":
		return automationBackendFramebuffer, nil
	case "window":
		return automationBackendWindow, nil
	default:
		return automationBackendAuto, fmt.Errorf("invalid -automation-backend %q (must be auto, framebuffer, or window)", s)
	}
}

func parseAutomationCaptureBackend(s string) (automationBackendMode, error) {
	return parseAutomationBackend(s)
}

func parseAutomationInputBackend(s string) (automationBackendMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return automationBackendAuto, nil
	case "direct", "framebuffer":
		return automationBackendFramebuffer, nil
	case "window":
		return automationBackendWindow, nil
	default:
		return automationBackendAuto, fmt.Errorf("invalid -automation-input-backend %q (must be auto, direct, or window)", s)
	}
}

func (m automationBackendMode) String() string {
	switch m {
	case automationBackendFramebuffer:
		return "framebuffer"
	case automationBackendWindow:
		return "window"
	default:
		return "auto"
	}
}

func (m automationBackendMode) inputString() string {
	switch m {
	case automationBackendFramebuffer:
		return "direct"
	case automationBackendWindow:
		return "window"
	default:
		return "auto"
	}
}

func (m automationBackendMode) captureMode(headed bool) string {
	switch m {
	case automationBackendFramebuffer:
		return "private-framebuffer"
	case automationBackendWindow:
		return "window"
	default:
		if headed {
			return "window"
		}
		return "private-framebuffer"
	}
}

func (m automationBackendMode) keyboardMode() string {
	switch m {
	case automationBackendWindow:
		return "cgevent"
	case automationBackendFramebuffer:
		return "direct"
	default:
		return "auto"
	}
}

func (m automationBackendMode) mouseMode() string {
	switch m {
	case automationBackendWindow:
		return "cgevent"
	case automationBackendFramebuffer:
		return "direct"
	default:
		return "auto"
	}
}

func combinedAutomationBackend(capture, input automationBackendMode) string {
	if capture == automationBackendAuto && input == automationBackendAuto {
		return automationBackendAuto.String()
	}
	if capture == automationBackendFramebuffer && input == automationBackendFramebuffer {
		return automationBackendFramebuffer.String()
	}
	if capture == automationBackendWindow && input == automationBackendWindow {
		return automationBackendWindow.String()
	}
	return "mixed"
}

func resolveAutomationBackends() (capture, input automationBackendMode) {
	combined, err := parseAutomationBackend(automationBackend)
	if err != nil {
		combined = automationBackendAuto
	}
	capture = combined
	input = combined
	if strings.TrimSpace(automationCaptureBackend) != "" {
		if mode, err := parseAutomationCaptureBackend(automationCaptureBackend); err == nil {
			capture = mode
		}
	}
	if strings.TrimSpace(automationInputBackend) != "" {
		if mode, err := parseAutomationInputBackend(automationInputBackend); err == nil {
			input = mode
		}
	}
	return capture, input
}
