package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"rsc.io/script"
)

func TestParseAnswerVisibleArgsTimeoutAndProgress(t *testing.T) {
	got, err := parseAnswerVisibleArgs([]string{
		"-timeout", "5s",
		"-progress", "Installing",
		"-progress", "Verifying",
		"User", "alice",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", got.timeout)
	}
	if !reflect.DeepEqual(got.progress, []string{"Installing", "Verifying"}) {
		t.Errorf("progress = %v", got.progress)
	}
	if got.delay != 1500*time.Millisecond {
		t.Errorf("delay default = %v, want 1500ms", got.delay)
	}
	if len(got.pairs) != 1 || got.pairs[0].prompt != "User" || got.pairs[0].answer != "alice" {
		t.Errorf("pairs = %#v", got.pairs)
	}
}

func TestParseAnswerVisibleArgsErrors(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"timeout missing value", []string{"-timeout"}, "-timeout requires"},
		{"timeout invalid", []string{"-timeout", "wat", "P", "a"}, "invalid timeout"},
		{"delay missing value", []string{"-delay"}, "-delay requires"},
		{"delay invalid", []string{"-delay", "wat", "P", "a"}, "invalid delay"},
		{"progress missing value", []string{"-progress"}, "-progress requires"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAnswerVisibleArgs(tc.args)
			if err == nil {
				t.Fatalf("err = nil, want %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseAnswerVisibleArgsOddPairs(t *testing.T) {
	cases := [][]string{
		{},
		{"justone"},
		{"a", "b", "c"},
	}
	for _, args := range cases {
		_, err := parseAnswerVisibleArgs(args)
		if !errors.Is(err, script.ErrUsage) {
			t.Errorf("parseAnswerVisibleArgs(%v) err = %v, want script.ErrUsage", args, err)
		}
	}
}

func TestParseAnswerVisibleArgsRequiresPairs(t *testing.T) {
	// -optional alone (no pairs) is still ErrUsage; the optional+skip-empty
	// path only short-circuits after pair processing filters everything out.
	if _, err := parseAnswerVisibleArgs([]string{"-optional"}); !errors.Is(err, script.ErrUsage) {
		t.Errorf("optional alone err = %v, want script.ErrUsage", err)
	}
}
