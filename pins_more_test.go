package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHandlePinsCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		wantSub string
	}{
		{name: "no args", args: nil, wantErr: true, wantSub: "usage: cove pins"},
		{name: "unknown subcommand", args: []string{"bogus"}, wantErr: true, wantSub: "unknown subcommand"},
		{name: "help short", args: []string{"-h"}, wantErr: false},
		{name: "help long", args: []string{"--help"}, wantErr: false},
		{name: "help word", args: []string{"help"}, wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handlePinsCommand(tt.args)
			if tt.wantErr && err == nil {
				t.Fatalf("handlePinsCommand(%v) = nil, want error", tt.args)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("handlePinsCommand(%v) = %v, want nil", tt.args, err)
			}
			if tt.wantSub != "" && err != nil && !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("handlePinsCommand(%v) err = %q, want substring %q", tt.args, err.Error(), tt.wantSub)
			}
		})
	}
}

func TestHandlePinsCommandListDispatchesToRunPinsList(t *testing.T) {
	pinsTestHome(t)
	if err := handlePinsCommand([]string{"list"}); err != nil {
		t.Fatalf("handlePinsCommand list: %v", err)
	}
}

func TestHandleUnpinNotPinnedBranch(t *testing.T) {
	pinsTestHome(t)
	if err := handleUnpinCommand([]string{"vm:never-pinned"}); err != nil {
		t.Fatalf("unpin missing entry: %v", err)
	}
}

func TestPinAndUnpinHelpFlag(t *testing.T) {
	pinsTestHome(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "pin -h", args: []string{"-h"}},
		{name: "pin --help", args: []string{"--help"}},
		{name: "unpin -h", args: []string{"-h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" pin", func(t *testing.T) {
			if err := handlePinCommand(tt.args); err != nil {
				t.Fatalf("handlePinCommand(%v) = %v, want nil", tt.args, err)
			}
		})
		t.Run(tt.name+" unpin", func(t *testing.T) {
			if err := handleUnpinCommand(tt.args); err != nil {
				t.Fatalf("handleUnpinCommand(%v) = %v, want nil", tt.args, err)
			}
		})
	}
}

func TestRunPinsListTableWithEntries(t *testing.T) {
	pinsTestHome(t)
	if err := handlePinCommand([]string{"image:foo:bar"}); err != nil {
		t.Fatalf("seed pin: %v", err)
	}
	var buf bytes.Buffer
	if err := runPinsList(nil, &buf); err != nil {
		t.Fatalf("runPinsList: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"REF", "ADDED", "image:foo:bar"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestRunPinsListExtraArgsRejected(t *testing.T) {
	pinsTestHome(t)
	var buf bytes.Buffer
	err := runPinsList([]string{"unexpected"}, &buf)
	if err == nil || !strings.Contains(err.Error(), "usage: cove pins list") {
		t.Fatalf("runPinsList(extra) = %v, want usage error", err)
	}
}
