package main

import (
	"strings"
	"testing"
)

func TestValidateNewVMName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantSub string
	}{
		{"empty", "", "enter a VM name"},
		{"whitespaceOnly", "   ", "enter a VM name"},
		{"slash", "foo/bar", "path separators"},
		{"dot", ".", "different VM name"},
		{"dotdot", "..", "different VM name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNewVMName(tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want %q", err, tt.wantSub)
			}
		})
	}

	if err := validateNewVMName("good-name"); err != nil {
		t.Fatalf("good-name err = %v, want nil", err)
	}
}
