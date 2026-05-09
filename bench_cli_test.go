package main

import (
	"strings"
	"testing"
)

func TestHandleBenchCommandNoArgsPrintsUsage(t *testing.T) {
	if err := handleBenchCommand(nil); err != nil {
		t.Fatalf("handleBenchCommand(nil) = %v, want nil", err)
	}
}

func TestHandleBenchCommandHelpArg(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		if err := handleBenchCommand([]string{arg}); err != nil {
			t.Errorf("handleBenchCommand(%q) = %v, want nil", arg, err)
		}
	}
}

func TestHandleBenchCommandUnknownSubcommand(t *testing.T) {
	err := handleBenchCommand([]string{"bogus"})
	if err == nil {
		t.Fatal("handleBenchCommand(bogus) = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown bench subcommand") {
		t.Errorf("error = %q, want contains 'unknown bench subcommand'", err)
	}
}

func TestRunBenchCompetitiveBadFlag(t *testing.T) {
	err := runBenchCompetitive([]string{"-nope"})
	if err == nil {
		t.Fatal("runBenchCompetitive(-nope) = nil, want flag parse error")
	}
}

func TestRunBenchCompetitiveExtraArgs(t *testing.T) {
	err := runBenchCompetitive([]string{"unexpected"})
	if err == nil {
		t.Fatal("runBenchCompetitive(unexpected) = nil, want error")
	}
	if !strings.Contains(err.Error(), "unexpected arguments") {
		t.Errorf("error = %q, want contains 'unexpected arguments'", err)
	}
}
