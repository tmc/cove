package main

import (
	"strings"
	"testing"
)

func TestSCKitSpikeRejectsInvalidIterations(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			err := runSCKitSpike([]string{"-n", value})
			if err == nil || !strings.Contains(err.Error(), "iterations must be positive") {
				t.Fatalf("runSCKitSpike(-n %s) error = %v", value, err)
			}
		})
	}
}
