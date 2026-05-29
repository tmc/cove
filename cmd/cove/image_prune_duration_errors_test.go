package main

import (
	"strings"
	"testing"
)

func TestParseImagePruneDurationErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantSub string
	}{
		{"badDays", "weekd", "parse -older-than"},
		{"badPlain", "frobnicate", "parse -older-than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseImagePruneDuration(tt.in)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want %q", err, tt.wantSub)
			}
		})
	}
}
