package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/metrics"
)

// TestResolveShellEnvSecretEnvVarUnset distinguishes the "env var unset"
// case from the "env var set to empty string" case. Unset propagates the
// secrets.Resolve "not set" error; set-to-empty hits the empty-value
// guard inside resolveShellEnv.
func TestResolveShellEnvSecretEnvVarUnset(t *testing.T) {
	const k = "R63_TEST_DEFINITELY_UNSET"
	t.Setenv(k, "x") // registers cleanup to restore prior state
	if err := os.Unsetenv(k); err != nil {
		t.Fatal(err)
	}
	_, err := resolveShellEnv(nil,
		[]string{"X=env://" + k},
		metrics.NewMasker(), &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
	if !strings.Contains(err.Error(), "not set") {
		t.Fatalf("error = %v, want 'not set'", err)
	}
}

// TestResolveShellEnvBadEntries covers parser edge cases that currently
// have no direct coverage: empty NAME via leading '=', and --env (which
// does NOT support URI schemes) receiving an env:// value verbatim.
func TestResolveShellEnvBadEntries(t *testing.T) {
	tests := []struct {
		name      string
		envs      []string
		secrets   []string
		wantErr   string
		wantValue map[string]string
	}{
		{
			name:    "empty NAME on --env",
			envs:    []string{"=value"},
			wantErr: "empty NAME",
		},
		{
			name:    "empty NAME on --secret-env",
			secrets: []string{"=value"},
			wantErr: "empty NAME",
		},
		{
			name:    "missing equals on --secret-env",
			secrets: []string{"NOEQUALS"},
			wantErr: "missing '='",
		},
		{
			name:      "env carries env:// verbatim (no resolution)",
			envs:      []string{"X=env://NEVER_RESOLVED"},
			wantValue: map[string]string{"X": "env://NEVER_RESOLVED"},
		},
		{
			name:    "uppercase ENV:// not recognized as URI",
			secrets: []string{"X=ENV://Y"},
			// isSecretURI uses prefix match on lowercase schemes; uppercase
			// is treated as a literal value, then the empty-value guard
			// does NOT fire because "ENV://Y" is non-empty.
			wantValue: map[string]string{"X": "ENV://Y"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveShellEnv(tt.envs, tt.secrets,
				metrics.NewMasker(), &bytes.Buffer{})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			for k, v := range tt.wantValue {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestResolveShellEnvFileMissing confirms file:// for a nonexistent path
// surfaces the secrets.Resolve error rather than a parser error.
func TestResolveShellEnvFileMissing(t *testing.T) {
	_, err := resolveShellEnv(nil,
		[]string{"X=file:///nonexistent/r63/path"},
		metrics.NewMasker(), &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
