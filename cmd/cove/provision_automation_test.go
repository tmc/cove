package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tmc/cove/internal/controlserver"
)

func TestLoginRetryAllowed(t *testing.T) {
	agentDown := fmt.Errorf("query console user: %w", errors.New("agent not connected"))
	noUser := fmt.Errorf("query console user: %w", controlserver.ErrNoConsoleUser)

	tests := []struct {
		name       string
		state      ScreenState
		screenErr  error
		consoleErr error
		want       bool
	}{
		{name: "login screen", state: ScreenStateLoginScreen, consoleErr: agentDown, want: true},
		{name: "desktop with conclusive empty console", state: ScreenStateDesktop, consoleErr: noUser, want: true},
		{name: "desktop with unreachable agent", state: ScreenStateDesktop, consoleErr: agentDown},
		{name: "desktop logged in", state: ScreenStateDesktop},
		{name: "screen detection failed", state: ScreenStateLoginScreen, screenErr: errors.New("capture failed"), consoleErr: noUser},
		{name: "unknown screen", state: ScreenStateUnknown, consoleErr: noUser},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := loginRetryAllowed(tt.state, tt.screenErr, tt.consoleErr)
			if got != tt.want {
				t.Fatalf("loginRetryAllowed() = %v (%s), want %v", got, why, tt.want)
			}
			if !got && why == "" {
				t.Fatal("loginRetryAllowed() denied without a reason")
			}
		})
	}
}
