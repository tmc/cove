package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredMemoryBytes(t *testing.T) {
	prevMemoryGB := memoryGB
	t.Cleanup(func() {
		memoryGB = prevMemoryGB
	})

	tests := []struct {
		name      string
		setup     func(t *testing.T) string
		globalGB  uint64
		wantBytes uint64
		wantErr   string
	}{
		{
			name: "config wins",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				if err := SaveVMConfig(dir, &VMConfig{MemoryGB: 6}); err != nil {
					t.Fatalf("SaveVMConfig: %v", err)
				}
				return dir
			},
			globalGB:  4,
			wantBytes: 6 * bytesPerGiB,
		},
		{
			name: "global fallback",
			setup: func(t *testing.T) string {
				t.Helper()
				return t.TempDir()
			},
			globalGB:  4,
			wantBytes: 4 * bytesPerGiB,
		},
		{
			name: "invalid config",
			setup: func(t *testing.T) string {
				t.Helper()
				dir := t.TempDir()
				path := filepath.Join(dir, "config.json")
				if err := os.WriteFile(path, []byte("{"), 0644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
				return dir
			},
			globalGB: 4,
			wantErr:  "load vm config:",
		},
		{
			name: "zero when unknown",
			setup: func(t *testing.T) string {
				t.Helper()
				return ""
			},
			globalGB:  0,
			wantBytes: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memoryGB = tt.globalGB
			vmDirectory := tt.setup(t)

			got, err := configuredMemoryBytes(vmDirectory)
			if tt.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
					t.Fatalf("configuredMemoryBytes() error = %v, want prefix %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("configuredMemoryBytes() error = %v", err)
			}
			if got != tt.wantBytes {
				t.Fatalf("configuredMemoryBytes() = %d, want %d", got, tt.wantBytes)
			}
		})
	}
}

func TestValidateMemoryTargetGB(t *testing.T) {
	tests := []struct {
		name            string
		targetGB        float64
		configuredBytes uint64
		wantErr         string
	}{
		{
			name:            "within configured memory",
			targetGB:        4,
			configuredBytes: 4 * bytesPerGiB,
		},
		{
			name:            "exceeds configured memory",
			targetGB:        5,
			configuredBytes: 4 * bytesPerGiB,
			wantErr:         "target size 5.00 GB exceeds configured memory 4.00 GB",
		},
		{
			name:     "unknown configured memory",
			targetGB: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMemoryTargetGB(tt.targetGB, tt.configuredBytes)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateMemoryTargetGB() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("validateMemoryTargetGB() error = nil, want error")
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("validateMemoryTargetGB() error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}
