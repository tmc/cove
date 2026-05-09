package main

import (
	"flag"
	"testing"
	"time"
)

func TestShortDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"negative clamped to zero", -5 * time.Second, "0s"},
		{"zero", 0, "0s"},
		{"sub-minute rounds to seconds", 12*time.Second + 400*time.Millisecond, "12s"},
		{"sub-minute rounds up", 12*time.Second + 600*time.Millisecond, "13s"},
		{"exact minute", time.Minute, "1m00s"},
		{"minutes and seconds", 2*time.Minute + 5*time.Second, "2m05s"},
		{"just under hour", 59*time.Minute + 30*time.Second, "59m30s"},
		{"exact hour", time.Hour, "1h00m"},
		{"hours and minutes", 3*time.Hour + 7*time.Minute, "3h07m"},
		{"hours truncate seconds", 2*time.Hour + 30*time.Minute + 45*time.Second, "2h30m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortDuration(tt.in)
			if got != tt.want {
				t.Errorf("shortDuration(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFlagWasProvided(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		flagToSet string
		check     string
		want      bool
	}{
		{"flag set on cli", []string{"-name", "alpha"}, "", "name", true},
		{"flag absent", []string{}, "", "name", false},
		{"different flag set", []string{"-other", "x"}, "", "name", false},
		{"empty-string value still counts", []string{"-name", ""}, "", "name", true},
		{"flag set via fs.Set", nil, "name", "name", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.String("name", "default", "")
			fs.String("other", "", "")
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if tt.flagToSet != "" {
				if err := fs.Set(tt.flagToSet, "via-set"); err != nil {
					t.Fatalf("set: %v", err)
				}
			}
			if got := flagWasProvided(fs, tt.check); got != tt.want {
				t.Errorf("flagWasProvided(%q) = %v, want %v (args=%v)", tt.check, got, tt.want, tt.args)
			}
		})
	}
}
