package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestIsVZAlreadyStoppedStopError(t *testing.T) {
	err := nsErrorSnapshot{
		domain:      "VZErrorDomain",
		code:        4,
		description: `Invalid virtual machine state transition. Transition from state "stopped" to state "stopping" is invalid.`,
	}
	if !isVZAlreadyStoppedStopError(err) {
		t.Fatal("isVZAlreadyStoppedStopError returned false")
	}
}

func TestIsVZAlreadyStoppedStopErrorRejectsOtherErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"wrong domain", nsErrorSnapshot{domain: "Other", code: 4, description: `Transition from state "stopped" to state "stopping" is invalid.`}},
		{"wrong code", nsErrorSnapshot{domain: "VZErrorDomain", code: 6, description: `Transition from state "stopped" to state "stopping" is invalid.`}},
		{"wrong transition", nsErrorSnapshot{domain: "VZErrorDomain", code: 4, description: `Transition from state "running" to state "stopping" is invalid.`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if isVZAlreadyStoppedStopError(tt.err) {
				t.Fatal("isVZAlreadyStoppedStopError returned true")
			}
		})
	}
}

func TestNSErrorSnapshotError(t *testing.T) {
	tests := []struct {
		name string
		snap nsErrorSnapshot
		want string
	}{
		{"empty", nsErrorSnapshot{}, "virtualization error"},
		{"domain only", nsErrorSnapshot{domain: "VZ", code: 7}, "domain=VZ code=7"},
		{"description only", nsErrorSnapshot{description: "boom"}, "boom"},
		{"all fields", nsErrorSnapshot{domain: "VZ", code: 1, description: "d", reason: "r"}, "domain=VZ code=1: d: r"},
		{"reason equals description suppressed", nsErrorSnapshot{description: "x", reason: "x"}, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.snap.Error(); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestSnapshotNSError(t *testing.T) {
	if got := snapshotNSError(nil); got != nil {
		t.Fatalf("nil in: got %v", got)
	}
	in := errors.New("plain")
	got := snapshotNSError(in)
	if got == nil || got.Error() != "plain" {
		t.Fatalf("plain error: got %v", got)
	}
	// Non-NSError should be re-wrapped (different identity).
	if got == in {
		t.Fatal("snapshotNSError returned same error instance")
	}
}

func TestPrintNSErrorSummary(t *testing.T) {
	capture := func(fn func()) string {
		r, w, _ := os.Pipe()
		old := os.Stdout
		os.Stdout = w
		fn()
		w.Close()
		os.Stdout = old
		b, _ := io.ReadAll(r)
		return string(b)
	}

	if printNSErrorSummary("p", errors.New("plain")) {
		t.Fatal("plain error: want false")
	}
	if printNSErrorSummary("p", (*nsErrorSnapshot)(nil)) {
		t.Fatal("nil pointer-typed: want false")
	}
	out := capture(func() {
		if !printNSErrorSummary("pre", nsErrorSnapshot{domain: "VZ", code: 9, description: "desc", reason: "rsn"}) {
			t.Fatal("snapshot: want true")
		}
	})
	for _, want := range []string{"pre: domain=VZ code=9", "pre: desc", "pre failure reason: rsn"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}
