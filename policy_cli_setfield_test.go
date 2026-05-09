package main

import (
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestSetPolicyFieldErrors(t *testing.T) {
	withTempHome(t)
	vmDir := vmconfig.Path("vm")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "wrong arg count", args: []string{"idle"}, want: "usage:"},
		{name: "unknown field", args: []string{"bogus", "1"}, want: "unknown policy field"},
		{name: "idle bad duration", args: []string{"idle", "nope"}, want: "parse idle timeout"},
		{name: "idle non-positive", args: []string{"idle", "0s"}, want: "idle timeout must be greater than zero"},
		{name: "idle negative", args: []string{"idle", "-5m"}, want: "idle timeout must be greater than zero"},
		{name: "max-age bad duration", args: []string{"max-age", "nope"}, want: "parse max age"},
		{name: "max-age non-positive", args: []string{"max-age", "0s"}, want: "max age must be greater than zero"},
		{name: "run-budget bad int", args: []string{"run-budget", "nope"}, want: "parse run budget"},
		{name: "run-budget non-positive", args: []string{"run-budget", "0"}, want: "run budget must be greater than zero"},
		{name: "run-budget negative", args: []string{"run-budget", "-1"}, want: "run budget must be greater than zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := setPolicyField("vm", vmDir, tt.args)
			if err == nil {
				t.Fatalf("setPolicyField(%v) = nil, want error containing %q", tt.args, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("setPolicyField(%v) error = %q, want substring %q", tt.args, err.Error(), tt.want)
			}
		})
	}
}

func TestSetPolicyFieldHappyPaths(t *testing.T) {
	withTempHome(t)
	vmDir := vmconfig.Path("vm")
	tests := []struct {
		name string
		args []string
		show string
	}{
		{name: "max-age", args: []string{"max-age", "24h"}, show: "24h0m0s"},
		{name: "run-budget", args: []string{"run-budget", "42"}, show: "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(t, func() error {
				return setPolicyField("vm", vmDir, tt.args)
			})
			if !strings.Contains(out, "Saved policy for vm") {
				t.Fatalf("setPolicyField(%v) stdout = %q, want Saved policy line", tt.args, out)
			}
			if !strings.Contains(out, "Policy file:") {
				t.Fatalf("setPolicyField(%v) stdout = %q, want Policy file line", tt.args, out)
			}
		})
	}
}
