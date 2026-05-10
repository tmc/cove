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
		{"empty", "", defaultAgentHealthInterval},
		{"valid", "250ms", 250 * time.Millisecond},
		{"unparseable", "not-a-duration", defaultAgentHealthInterval},
		{"negative", "-5s", defaultAgentHealthInterval},
		{"zero", "0s", defaultAgentHealthInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(agentHealthIntervalEnv, tt.env)
			if got := resolveAgentHealthInterval(); got != tt.want {
				t.Errorf("resolveAgentHealthInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}
