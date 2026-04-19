package main

import (
	"reflect"
	"testing"
	"time"
)

func TestParseRequireList(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace-only", "   ", nil},
		{"single", "go", []string{"go"}},
		{"multi", "go,xcode-cli,homebrew", []string{"go", "xcode-cli", "homebrew"}},
		{"trims-spaces", " go , xcode-cli , homebrew ", []string{"go", "xcode-cli", "homebrew"}},
		{"drops-empty", "go,,homebrew,", []string{"go", "homebrew"}},
		{"preserves-order-and-dups", "go,go,homebrew", []string{"go", "go", "homebrew"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRequireList(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseRequireList(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseReadyArgsDefaults(t *testing.T) {
	names, asJSON, timeout, useDaemon, err := parseReadyArgs(nil)
	if err != nil {
		t.Fatalf("parseReadyArgs(nil) error = %v", err)
	}
	want := []string{"agent-ping", "can-exec"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("default names = %#v, want %#v", names, want)
	}
	if asJSON {
		t.Fatalf("default --json = true, want false")
	}
	if timeout != 10*time.Second {
		t.Fatalf("default timeout = %s, want 10s", timeout)
	}
	if useDaemon {
		t.Fatalf("default --daemon = true, want false")
	}
}

func TestParseReadyArgsExplicit(t *testing.T) {
	args := []string{"--require", "go,xcode-cli", "--json", "--timeout", "5s", "--daemon"}
	names, asJSON, timeout, useDaemon, err := parseReadyArgs(args)
	if err != nil {
		t.Fatalf("parseReadyArgs error = %v", err)
	}
	if !reflect.DeepEqual(names, []string{"go", "xcode-cli"}) {
		t.Fatalf("names = %#v, want [go xcode-cli]", names)
	}
	if !asJSON {
		t.Fatalf("--json not parsed")
	}
	if timeout != 5*time.Second {
		t.Fatalf("timeout = %s, want 5s", timeout)
	}
	if !useDaemon {
		t.Fatalf("--daemon not parsed")
	}
}

func TestParseReadyArgsRejectsPositional(t *testing.T) {
	if _, _, _, _, err := parseReadyArgs([]string{"--require", "go", "leftover"}); err == nil {
		t.Fatalf("expected error for unexpected positional argument")
	}
}

func TestParseReadyArgsBadTimeout(t *testing.T) {
	if _, _, _, _, err := parseReadyArgs([]string{"--timeout", "not-a-duration"}); err == nil {
		t.Fatalf("expected error for invalid --timeout value")
	}
}

func TestReadyExitCode(t *testing.T) {
	cases := []struct {
		name    string
		agentOK bool
		results []readyResult
		want    int
	}{
		{"agent-down", false, nil, readyExitUnreachable},
		{"agent-down-with-results-still-2", false, []readyResult{{Name: "go", OK: false}}, readyExitUnreachable},
		{"all-pass", true, []readyResult{{Name: "go", OK: true}, {Name: "brew", OK: true}}, readyExitOK},
		{"any-fail", true, []readyResult{{Name: "go", OK: true}, {Name: "brew", OK: false}}, readyExitFailed},
		{"empty-checks-with-agent-ok", true, nil, readyExitOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readyExitCode(tc.agentOK, tc.results)
			if got != tc.want {
				t.Fatalf("readyExitCode(%v, %#v) = %d, want %d", tc.agentOK, tc.results, got, tc.want)
			}
		})
	}
}

func TestResolveReadyCheckBuiltin(t *testing.T) {
	c := resolveReadyCheck("go")
	if c.Name != "go" {
		t.Fatalf("Name = %q, want go", c.Name)
	}
	if !reflect.DeepEqual(c.Args, []string{"go", "version"}) {
		t.Fatalf("Args = %#v, want [go version]", c.Args)
	}
}

func TestResolveReadyCheckUnknownFallsBackToWhich(t *testing.T) {
	c := resolveReadyCheck("rustc")
	if c.Name != "rustc" {
		t.Fatalf("Name = %q, want rustc", c.Name)
	}
	if !reflect.DeepEqual(c.Args, []string{"which", "rustc"}) {
		t.Fatalf("Args = %#v, want [which rustc]", c.Args)
	}
}

func TestPickReadyDetail(t *testing.T) {
	cases := []struct {
		name     string
		stdout   string
		stderr   string
		exitCode int
		want     string
	}{
		{"stdout-wins", "go version go1.22.0 darwin/arm64\n", "warn\n", 0, "go version go1.22.0 darwin/arm64"},
		{"stderr-fallback", "", "command not found: brew\n", 1, "command not found: brew"},
		{"exit-status-when-no-output", "", "", 2, "exit status 2"},
		{"empty-on-success", "", "", 0, ""},
		{"skips-leading-blank-lines", "\n\nhello\nworld\n", "", 0, "hello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickReadyDetail(tc.stdout, tc.stderr, tc.exitCode)
			if got != tc.want {
				t.Fatalf("pickReadyDetail(%q, %q, %d) = %q, want %q", tc.stdout, tc.stderr, tc.exitCode, got, tc.want)
			}
		})
	}
}

func TestAgentStatus(t *testing.T) {
	if agentStatus(true) != "ok" {
		t.Fatalf("agentStatus(true) != ok")
	}
	if agentStatus(false) != "unreachable" {
		t.Fatalf("agentStatus(false) != unreachable")
	}
}
