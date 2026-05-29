package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleBenchCommandNoArgsPrintsUsage(t *testing.T) {
	env := commandTestEnv()
	if err := handleBenchCommand(env, nil); err != nil {
		t.Fatalf("handleBenchCommand(nil) = %v, want nil", err)
	}
	if got := env.Stderr.(*bytes.Buffer).String(); !strings.Contains(got, "Usage: cove bench") {
		t.Fatalf("stderr = %q, want usage", got)
	}
}

func TestHandleBenchCommandHelpArg(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		if err := handleBenchCommand(commandTestEnv(), []string{arg}); err != nil {
			t.Errorf("handleBenchCommand(%q) = %v, want nil", arg, err)
		}
	}
}

func TestHandleBenchCommandUnknownSubcommand(t *testing.T) {
	err := handleBenchCommand(commandTestEnv(), []string{"bogus"})
	if err == nil {
		t.Fatal("handleBenchCommand(bogus) = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown bench subcommand") {
		t.Errorf("error = %q, want contains 'unknown bench subcommand'", err)
	}
}

func TestRunBenchCompetitiveBadFlag(t *testing.T) {
	err := runBenchCompetitive(commandTestEnv(), []string{"-nope"})
	if err == nil {
		t.Fatal("runBenchCompetitive(-nope) = nil, want flag parse error")
	}
}

func TestRunBenchCompetitiveHelp(t *testing.T) {
	if err := runBenchCompetitive(commandTestEnv(), []string{"-h"}); err != nil {
		t.Fatalf("runBenchCompetitive(-h) = %v, want nil", err)
	}
}

func TestRunBenchCompetitiveDryRunDoesNotWriteDefaults(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(): %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	env := commandTestEnv()
	if err := runBenchCompetitive(env, []string{"-dry-run"}); err != nil {
		t.Fatalf("runBenchCompetitive(-dry-run): %v", err)
	}
	out := env.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "benchmark dry run") {
		t.Fatalf("output = %q, want dry run summary", out)
	}
	for _, path := range []string{
		filepath.Join(dir, "docs", "benchmarks", "results-2026-05-cove.json"),
		filepath.Join(dir, "docs", "benchmarks", "competitive-2026-05.md"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stat %s = %v, want not exist", path, err)
		}
	}
}

func TestRunBenchCompetitiveExtraArgs(t *testing.T) {
	err := runBenchCompetitive(commandTestEnv(), []string{"unexpected"})
	if err == nil {
		t.Fatal("runBenchCompetitive(unexpected) = nil, want error")
	}
	if !strings.Contains(err.Error(), "unexpected arguments") {
		t.Errorf("error = %q, want contains 'unexpected arguments'", err)
	}
}
