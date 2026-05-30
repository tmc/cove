package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pw "github.com/tmc/cove/internal/password"
)

func TestExtractFleetRunPolicy(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantPolicy string
		wantArgs   []string
		wantErr    string
	}{
		{
			name:     "no policy",
			args:     []string{"--", "echo", "hi"},
			wantArgs: []string{"--", "echo", "hi"},
		},
		{
			name:       "long flag separate",
			args:       []string{"--policy", "least-loaded", "--", "echo"},
			wantPolicy: "least-loaded",
			wantArgs:   []string{"--", "echo"},
		},
		{
			name:       "single-dash separate",
			args:       []string{"-policy", "round-robin", "echo"},
			wantPolicy: "round-robin",
			wantArgs:   []string{"echo"},
		},
		{
			name:       "long flag equals",
			args:       []string{"--policy=least-loaded", "echo"},
			wantPolicy: "least-loaded",
			wantArgs:   []string{"echo"},
		},
		{
			name:       "single-dash equals",
			args:       []string{"-policy=rr", "echo"},
			wantPolicy: "rr",
			wantArgs:   []string{"echo"},
		},
		{
			name:    "missing value",
			args:    []string{"--policy"},
			wantErr: "requires a value",
		},
		{
			name:       "last-wins on duplicate",
			args:       []string{"--policy=a", "--policy", "b", "x"},
			wantPolicy: "b",
			wantArgs:   []string{"x"},
		},
		{
			name:     "empty args",
			args:     nil,
			wantArgs: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, runArgs, err := extractFleetRunPolicy(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if policy != tt.wantPolicy {
				t.Fatalf("policy = %q, want %q", policy, tt.wantPolicy)
			}
			if !reflect.DeepEqual(runArgs, tt.wantArgs) {
				t.Fatalf("runArgs = %#v, want %#v", runArgs, tt.wantArgs)
			}
		})
	}
}

func TestFleetRunForkFrom(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "separate", args: []string{"-fork-from", "base:latest"}, want: "base:latest"},
		{name: "equals", args: []string{"--fork-from=base:v1"}, want: "base:v1"},
		{name: "after separator", args: []string{"--", "-fork-from", "base:v2"}, want: "base:v2"},
		{name: "missing", args: []string{"-linux"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fleetRunForkFrom(tt.args); got != tt.want {
				t.Fatalf("fleetRunForkFrom(%#v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestValidateKCPasswordFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	if err := os.WriteFile(good, pw.EncodeKC("hunter2"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateKCPasswordFile(good, "hunter2"); err != nil {
		t.Fatalf("good file: %v", err)
	}

	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(bad, []byte("not-kcpassword"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateKCPasswordFile(bad, "hunter2"); err == nil ||
		!strings.Contains(err.Error(), "encoded bytes do not match") {
		t.Fatalf("bad file: err = %v, want 'encoded bytes do not match'", err)
	}

	missing := filepath.Join(dir, "missing")
	if err := validateKCPasswordFile(missing, "hunter2"); err == nil ||
		!strings.Contains(err.Error(), "read kcpassword") {
		t.Fatalf("missing file: err = %v, want 'read kcpassword'", err)
	}
}
