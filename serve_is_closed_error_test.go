package main

import (
	"errors"
	"testing"
)

func TestIsClosedError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("connection refused"), false},
		{"closed", errors.New("write: use of closed network connection"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClosedError(tt.err); got != tt.want {
				t.Fatalf("isClosedError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
