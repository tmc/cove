package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestLookupCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantOK   bool
	}{
		{name: "primary name", input: "build", wantName: "build", wantOK: true},
		{name: "alias maps to primary", input: "ls", wantName: "list", wantOK: true},
		{name: "rm alias to primary", input: "destroy", wantName: "rm", wantOK: true},
		{name: "doctor alias", input: "doctor", wantName: "verify", wantOK: true},
		{name: "unknown command", input: "no-such-command", wantOK: false},
		{name: "empty string", input: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok := lookupCommand(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("lookupCommand(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if !ok {
				if spec != nil {
					t.Fatalf("lookupCommand(%q) returned non-nil spec when ok=false", tt.input)
				}
				return
			}
			if spec.Name != tt.wantName {
				t.Fatalf("lookupCommand(%q) name = %q, want %q", tt.input, spec.Name, tt.wantName)
			}
		})
	}
}

func TestCommandNamesContainsCoreCommands(t *testing.T) {
	names := commandNames()
	want := []string{"build", "run", "install", "list", "ls", "rm", "destroy", "help", "support", "version"}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("commandNames missing %q", w)
		}
	}
}

func TestRunRegisteredCommandNilSpec(t *testing.T) {
	tests := []struct {
		name string
		spec *commandSpec
	}{
		{name: "nil spec", spec: nil},
		{name: "spec with nil Run", spec: &commandSpec{Name: "x"}},
	}
	env := commandEnv{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runRegisteredCommand(env, tt.spec, "x", nil)
			if got != 2 {
				t.Fatalf("runRegisteredCommand(%v) = %d, want 2", tt.spec, got)
			}
		})
	}
}

func TestRunRegisteredCommandRunsSpec(t *testing.T) {
	called := false
	spec := &commandSpec{
		Name: "fake",
		Run: func(env commandEnv, name string, args []string) int {
			called = true
			if name != "alias" {
				t.Errorf("name = %q, want alias", name)
			}
			if len(args) != 1 || args[0] != "arg1" {
				t.Errorf("args = %v, want [arg1]", args)
			}
			return 7
		},
	}
	got := runRegisteredCommand(commandEnv{}, spec, "alias", []string{"arg1"})
	if !called {
		t.Fatal("Run was not called")
	}
	if got != 7 {
		t.Fatalf("got = %d, want 7", got)
	}
}

func TestNamespaceNoArgUsageExitCode(t *testing.T) {
	tests := []string{"agent-sandbox", "build", "fleet", "softreset", "storage", "support"}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			spec, ok := lookupCommand(name)
			if !ok {
				t.Fatalf("missing command %q", name)
			}
			var stderr bytes.Buffer
			got := runRegisteredCommand(commandEnv{Stderr: &stderr}, spec, name, nil)
			if got != 2 {
				t.Fatalf("%s no-arg exit = %d, want 2; stderr=%q", name, got, stderr.String())
			}
			if !strings.Contains(stderr.String(), "error:") {
				t.Fatalf("%s stderr = %q, want error", name, stderr.String())
			}
		})
	}
}

func TestCommandError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantRC  int
		wantOut string
	}{
		{name: "nil error", err: nil, wantRC: 0, wantOut: ""},
		{name: "real error", err: errors.New("boom"), wantRC: 1, wantOut: "error: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			env := commandEnv{Stderr: &buf}
			got := commandError(env, tt.err)
			if got != tt.wantRC {
				t.Fatalf("commandError rc = %d, want %d", got, tt.wantRC)
			}
			if tt.wantOut == "" {
				if buf.Len() != 0 {
					t.Fatalf("stderr = %q, want empty", buf.String())
				}
			} else if !strings.Contains(buf.String(), tt.wantOut) {
				t.Fatalf("stderr = %q, want substring %q", buf.String(), tt.wantOut)
			}
		})
	}
}
