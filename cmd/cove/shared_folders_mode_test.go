package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestSetSharedFolderMode(t *testing.T) {
	dir := t.TempDir()
	folders := []SharedFolderEntry{
		{Path: filepath.Join(dir, "a"), Tag: "alpha", ReadOnly: true},
		{Path: filepath.Join(dir, "b"), Tag: "beta"},
	}
	for _, tt := range []struct {
		name, selector string
		readOnly       bool
		index          int
	}{
		{"tag to writable", "alpha", false, 0},
		{"path to readonly", folders[1].Path, true, 1},
		{"unchanged", "alpha", true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := append([]SharedFolderEntry(nil), folders...)
			updated, entry, err := setSharedFolderMode(folders, tt.selector, tt.readOnly)
			if err != nil {
				t.Fatal(err)
			}
			want := append([]SharedFolderEntry(nil), folders...)
			want[tt.index].ReadOnly = tt.readOnly
			if !reflect.DeepEqual(updated, want) || entry != want[tt.index] {
				t.Fatalf("updated = %v, entry = %v, want %v", updated, entry, want)
			}
			if !reflect.DeepEqual(folders, original) {
				t.Fatal("modified input folders")
			}
		})
	}
	if _, _, err := setSharedFolderMode(folders, "missing", false); !errors.Is(err, ErrSharedFolderNotFound) {
		t.Fatalf("missing folder error = %v", err)
	}
}

func TestParseSharedFolderMode(t *testing.T) {
	for _, tt := range []struct {
		input   string
		want    bool
		wantErr bool
	}{{"ro", true, false}, {"rw", false, false}, {"", false, true}, {"invalid", false, true}} {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseSharedFolderMode(tt.input)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("parse mode = %v, %v", got, err)
			}
		})
	}
}

func TestSharedFolderModePersistsAndApplies(t *testing.T) {
	for _, tt := range []struct {
		name    string
		resp    *controlpb.ControlResponse
		wantErr string
	}{
		{"live", &controlpb.ControlResponse{Success: true, Data: "applied 1 shared folder(s)"}, ""},
		{"stopped", &controlpb.ControlResponse{Error: "vm is not running"}, ""},
		{"missing device", &controlpb.ControlResponse{Error: "shared folders device not found"}, "saved but not live-applied"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vmDir := shortSharedFolderVMDir(t)
			folders := []SharedFolderEntry{{Path: t.TempDir(), Tag: "work"}}
			if err := saveSharedFolders(vmDir, folders); err != nil {
				t.Fatal(err)
			}
			stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{{wantType: "shared-folders-apply", resp: tt.resp}})
			defer stop()
			err := handleVMSharedFolderMode(vmDir, "work", true)
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("mode error = %v, want %q", err, tt.wantErr)
			}
			got := LoadSharedFolders(vmDir)
			folders[0].ReadOnly = true
			if !reflect.DeepEqual(got, folders) {
				t.Fatalf("saved folders = %v, want %v", got, folders)
			}
		})
	}
}
