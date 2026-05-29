package main

import (
	"strings"
	"testing"
)

func TestParseSoftresetRunAllArgsRejections(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{"missingVM", nil, "usage:"},
		{"extraArg", []string{"vm", "extra"}, "usage:"},
		{"badFilter", []string{"vm", "--filter=bogus"}, "unknown softreset probe"},
		{"zeroTimeout", []string{"vm", "--timeout=0s"}, "--timeout must be positive"},
		{"negativeTimeout", []string{"vm", "--timeout=-5s"}, "--timeout must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSoftresetRunAllArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want %q", err, tt.wantSub)
			}
		})
	}
}
