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
		{name: "empty returns default", env: "", want: defaultAgentHealthInterval},
		{name: "valid override", env: "5s", want: 5 * time.Second},
		{name: "valid sub-second override", env: "250ms", want: 250 * time.Millisecond},
		{name: "unparseable falls back", env: "not-a-duration", want: defaultAgentHealthInterval},
		{name: "zero falls back", env: "0s", want: defaultAgentHealthInterval},
		{name: "negative falls back", env: "-1s", want: defaultAgentHealthInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(agentHealthIntervalEnv, tt.env)
			got := resolveAgentHealthInterval()
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
