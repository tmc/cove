package main

import (
	"testing"
	"time"
)

func TestAgentObservationFresh(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name     string
		observed time.Time
		want     bool
	}{
		{"unknown", time.Time{}, false},
		{"current", now, true},
		{"boundary", now.Add(-45 * time.Second), true},
		{"stale", now.Add(-45*time.Second - time.Nanosecond), false},
		{"future", now.Add(time.Nanosecond), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentObservationFresh(tt.observed, now); got != tt.want {
				t.Fatalf("fresh = %v, want %v", got, tt.want)
			}
		})
	}
}
