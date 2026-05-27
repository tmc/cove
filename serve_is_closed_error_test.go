package main

import (
	"errors"
	"fmt"
	"net"
	"os"
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
		{"net closed", net.ErrClosed, true},
		{"wrapped net closed", fmt.Errorf("write: %w", net.ErrClosed), true},
		{"os closed", &os.PathError{Op: "read", Path: "listener", Err: os.ErrClosed}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClosedError(tt.err); got != tt.want {
				t.Fatalf("isClosedError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
