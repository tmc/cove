package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
)

func TestRefreshAppliedSharedFolders(t *testing.T) {
	tests := []struct {
		name    string
		changed bool
		mounted bool
		err     error
		want    string
	}{
		{"unchanged mounted", false, false, nil, ""},
		{"unchanged missing mount", false, true, nil, "; remounted in guest"},
		{"changed", true, true, nil, "; remounted in guest"},
		{"unavailable agent", false, false, errors.New("guest agent unavailable"), ""},
		{"failure", true, false, errors.New("busy"), "; guest remount warning: busy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			got := refreshAppliedSharedFolders(tt.changed, func(force bool) (bool, error) {
				calls++
				if force != tt.changed {
					t.Fatalf("force = %v, want %v", force, tt.changed)
				}
				return tt.mounted, tt.err
			})
			if calls != 1 || got != tt.want {
				t.Fatalf("calls = %d, message = %q, want one call and %q", calls, got, tt.want)
			}
		})
	}
}

func TestSharedFolderNativeShareMatches(t *testing.T) {
	path := t.TempDir()
	other := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	dir := vz.NewSharedDirectoryWithURLReadOnly(foundation.NewURLFileURLWithPath(path), true)
	dict := newDictFromSlices([]objectivec.IObject{objectivec.ObjectFromID(dir.ID)}, []objectivec.IObject{objectivec.ObjectFromID(objc.String("workspace"))})
	share := vz.NewMultipleDirectoryShareWithDirectories(&dict)
	tests := []struct {
		name    string
		folders []SharedFolderEntry
		want    bool
	}{
		{"same", []SharedFolderEntry{{Tag: "workspace", Path: path, ReadOnly: true}}, true},
		{"canonical path", []SharedFolderEntry{{Tag: "workspace", Path: alias, ReadOnly: true}}, true},
		{"read only changed", []SharedFolderEntry{{Tag: "workspace", Path: path}}, false},
		{"path changed", []SharedFolderEntry{{Tag: "workspace", Path: other, ReadOnly: true}}, false},
		{"tag changed", []SharedFolderEntry{{Tag: "output", Path: path, ReadOnly: true}}, false},
		{"removed", nil, false},
		{"added", []SharedFolderEntry{{Tag: "workspace", Path: path, ReadOnly: true}, {Tag: "output", Path: other}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sharedFolderNativeShareMatches(&share.VZDirectoryShare, tt.folders); got != tt.want {
				t.Fatalf("matches = %v, want %v", got, tt.want)
			}
		})
	}
	if sharedFolderNativeShareMatches(nil, nil) {
		t.Fatal("nil share matches")
	}
	emptyDict := foundation.NewNSDictionary()
	empty := vz.NewMultipleDirectoryShareWithDirectories(&emptyDict)
	if !sharedFolderNativeShareMatches(&empty.VZDirectoryShare, nil) {
		t.Fatal("empty share does not match empty desired set")
	}
	single := vz.NewSingleDirectoryShareWithDirectory(&dir)
	if sharedFolderNativeShareMatches(&single.VZDirectoryShare, tests[0].folders) {
		t.Fatal("single directory share matches multiple directory share")
	}
}
