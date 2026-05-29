package main

import (
	"strings"
	"testing"
)

func TestResolveForkInvocation(t *testing.T) {
	tests := []struct {
		name         string
		fromRef      string
		snapshotFlag string
		posArgs      []string
		wantParent   string
		wantChild    string
		wantSnap     string
		wantErr      string
	}{
		// Positional form
		{"positional plain", "", "", []string{"p", "c"}, "p", "c", "", ""},
		{"positional with -snapshot", "", "snap1", []string{"p", "c"}, "p", "c", "snap1", ""},
		{"positional missing args", "", "", []string{"p"}, "", "", "", "usage:"},
		{"positional too many args", "", "", []string{"p", "c", "extra"}, "", "", "", "usage:"},

		// --from form
		{"from plain", "p", "", []string{"c"}, "p", "c", "", ""},
		{"from with @snap only", "p@snap1", "", []string{"c"}, "p", "c", "snap1", ""},
		{"from plain + -snapshot", "p", "snap1", []string{"c"}, "p", "c", "snap1", ""},
		{"from + -snapshot agree (redundant)", "p@snap1", "snap1", []string{"c"}, "p", "c", "snap1", ""},
		{"from + -snapshot conflict", "p@snap1", "snap2", []string{"c"}, "", "", "", "conflicts"},
		{"from missing positional child", "p", "", nil, "", "", "", "exactly one"},
		{"from with two positional", "p", "", []string{"c", "extra"}, "", "", "", "exactly one"},
		{"from invalid ref", "p@", "", []string{"c"}, "", "", "", "empty snapshot name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parent, child, snap, err := resolveForkInvocation(tc.fromRef, tc.snapshotFlag, tc.posArgs)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if parent != tc.wantParent || child != tc.wantChild || snap != tc.wantSnap {
					t.Errorf("got (%q, %q, %q), want (%q, %q, %q)",
						parent, child, snap, tc.wantParent, tc.wantChild, tc.wantSnap)
				}
				return
			}
			if err == nil {
				t.Fatalf("err = nil, want substring %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParseForkRef(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantParent   string
		wantSnapshot string
		wantErr      string // substring; empty means no error
	}{
		{"plain parent", "macos-base", "macos-base", "", ""},
		{"parent and snapshot", "macos-base@clean", "macos-base", "clean", ""},
		{"snapshot with hyphens", "p@a-b-c", "p", "a-b-c", ""},
		{"snapshot with digits", "parent@v123", "parent", "v123", ""},
		{"empty input", "", "", "", "must not be empty"},
		{"empty parent", "@clean", "", "", "missing parent name"},
		{"empty snapshot after @", "parent@", "", "", "empty snapshot name"},
		{"multiple @", "a@b@c", "", "", "multiple '@'"},
		{"snapshot with slash rejected", "parent@a/b", "", "", "must not contain path separators"},
		{"snapshot is dotdot rejected", "parent@..", "", "", "must not be"},
		{"snapshot is dot rejected", "parent@.", "", "", "must not be"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parent, snapshot, err := parseForkRef(tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("parseForkRef(%q) err = %v, want nil", tc.input, err)
				}
				if parent != tc.wantParent {
					t.Errorf("parent = %q, want %q", parent, tc.wantParent)
				}
				if snapshot != tc.wantSnapshot {
					t.Errorf("snapshot = %q, want %q", snapshot, tc.wantSnapshot)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseForkRef(%q) err = nil, want error containing %q", tc.input, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
