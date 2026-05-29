package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseClickTextOptions(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantText   string
		wantRegion string
		wantTO     time.Duration
		wantErr    string
	}{
		{
			name:     "default timeout, single word",
			args:     []string{"Continue"},
			wantText: "Continue",
			wantTO:   10 * time.Second,
		},
		{
			name:     "multiple word text joined",
			args:     []string{"Get", "Started"},
			wantText: "Get Started",
			wantTO:   10 * time.Second,
		},
		{
			name:    "missing text",
			args:    []string{"-timeout", "1s"},
			wantErr: "requires text argument",
		},
		{
			name:    "timeout missing value",
			args:    []string{"-timeout"},
			wantErr: "-timeout requires a value",
		},
		{
			name:    "timeout invalid",
			args:    []string{"-timeout", "abc", "Foo"},
			wantErr: "invalid timeout",
		},
		{
			name:    "region missing value",
			args:    []string{"-region"},
			wantErr: "-region requires a value",
		},
		{
			name:     "custom timeout",
			args:     []string{"-timeout", "3s", "OK"},
			wantText: "OK",
			wantTO:   3 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, region, to, err := parseClickTextOptions(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
			if region != tt.wantRegion {
				t.Errorf("region = %q, want %q", region, tt.wantRegion)
			}
			if to != tt.wantTO {
				t.Errorf("timeout = %v, want %v", to, tt.wantTO)
			}
		})
	}
}

func TestStartupNorm(t *testing.T) {
	tests := []struct {
		name      string
		v, size   float64
		want      float64
	}{
		{"zero size", 5, 0, 0},
		{"negative size", 5, -1, 0},
		{"normal mid", 50, 100, 0.5},
		{"clamp negative", -10, 100, 0},
		{"clamp over one", 200, 100, 1},
		{"exact one", 100, 100, 1},
		{"exact zero", 0, 100, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := startupNorm(tt.v, tt.size)
			if got != tt.want {
				t.Errorf("startupNorm(%v, %v) = %v, want %v", tt.v, tt.size, got, tt.want)
			}
		})
	}
}
