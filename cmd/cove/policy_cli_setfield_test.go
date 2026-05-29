package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmpolicy"
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
			err := setPolicyField(new(bytes.Buffer), "vm", vmDir, tt.args)
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
			var out bytes.Buffer
			if err := setPolicyField(&out, "vm", vmDir, tt.args); err != nil {
				t.Fatalf("setPolicyField: %v", err)
			}
			if !strings.Contains(out.String(), "Saved policy for vm") {
				t.Fatalf("setPolicyField(%v) stdout = %q, want Saved policy line", tt.args, out.String())
			}
			if !strings.Contains(out.String(), "Policy file:") {
				t.Fatalf("setPolicyField(%v) stdout = %q, want Policy file line", tt.args, out.String())
			}
		})
	}
}

func TestSetPolicyFieldPreservesExistingFields(t *testing.T) {
	withTempHome(t)
	vmDir := vmconfig.Path("vm")
	start := vmpolicy.Policy{IdleTimeout: time.Minute, MaxAge: time.Hour, RunBudget: 3}
	if err := vmpolicy.Save(vmDir, start); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want vmpolicy.Policy
	}{
		{
			name: "idle override",
			args: []string{"idle", "2m"},
			want: vmpolicy.Policy{IdleTimeout: 2 * time.Minute, MaxAge: time.Hour, RunBudget: 3},
		},
		{
			name: "run budget override",
			args: []string{"run-budget", "1"},
			want: vmpolicy.Policy{IdleTimeout: 2 * time.Minute, MaxAge: time.Hour, RunBudget: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := setPolicyField(new(bytes.Buffer), "vm", vmDir, tt.args); err != nil {
				t.Fatalf("setPolicyField: %v", err)
			}
			got, err := vmpolicy.Load(vmDir)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got != tt.want {
				t.Fatalf("policy = %#v, want %#v", got, tt.want)
			}
		})
	}
}
