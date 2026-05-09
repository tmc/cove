package main

import (
	"testing"
	"time"
)

func TestResolveAgentHealthInterval(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"empty returns default", "", defaultAgentHealthInterval},
		{"valid duration", "5s", 5 * time.Second},
		{"valid millis", "250ms", 250 * time.Millisecond},
		{"unparseable falls back", "not-a-duration", defaultAgentHealthInterval},
		{"zero falls back", "0s", defaultAgentHealthInterval},
		{"negative falls back", "-1s", defaultAgentHealthInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(agentHealthIntervalEnv, tt.env)
			got := resolveAgentHealthInterval()
			if got != tt.want {
				t.Errorf("resolveAgentHealthInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}
