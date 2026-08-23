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
		{name: "login screen with a logged-in console user", state: ScreenStateLoginScreen, want: false},
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

func TestLoginTypeDecision(t *testing.T) {
	agentDown := fmt.Errorf("query console user: %w", errors.New("agent not connected"))
	noUser := fmt.Errorf("query console user: %w", controlserver.ErrNoConsoleUser)

	tests := []struct {
		name       string
		state      ScreenState
		screenErr  error
		user       string
		consoleErr error
		streak     int
		want       loginTypeAction
	}{
		{
			name:   "logged-in desktop misclassified as login screen",
			state:  ScreenStateLoginScreen,
			user:   "tmc",
			streak: loginScreenConfirmations,
			want:   loginTypeAbort,
		},
		{
			name:       "login screen with conclusive empty console",
			state:      ScreenStateLoginScreen,
			consoleErr: noUser,
			streak:     1,
			want:       loginTypeType,
		},
		{
			name:       "first login screen sample with unreachable agent",
			state:      ScreenStateLoginScreen,
			consoleErr: agentDown,
			streak:     1,
			want:       loginTypeWait,
		},
		{
			name:       "stable login screen with unreachable agent",
			state:      ScreenStateLoginScreen,
			consoleErr: agentDown,
			streak:     loginScreenConfirmations,
			want:       loginTypeType,
		},
		{
			name:       "desktop",
			state:      ScreenStateDesktop,
			consoleErr: noUser,
			streak:     0,
			want:       loginTypeWait,
		},
		{
			name:       "unreadable screen",
			state:      ScreenStateLoginScreen,
			screenErr:  errors.New("capture failed"),
			consoleErr: noUser,
			streak:     loginScreenConfirmations,
			want:       loginTypeWait,
		},
		{
			name:       "unknown screen",
			state:      ScreenStateUnknown,
			consoleErr: agentDown,
			streak:     0,
			want:       loginTypeWait,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := loginTypeDecision(tt.state, tt.screenErr, tt.user, tt.consoleErr, tt.streak)
			if got != tt.want {
				t.Fatalf("loginTypeDecision() = %v (%s), want %v", got, why, tt.want)
			}
			if got != loginTypeType && why == "" {
				t.Fatal("loginTypeDecision() withheld typing without a reason")
			}
		})
	}
}
