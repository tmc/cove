package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
)

func TestToolbarSharedFoldersSequencingAndErrors(t *testing.T) {
	origPresenter := guiAlertPresenter
	defer func() { guiAlertPresenter = origPresenter }()
	origDispatch := dispatchAsyncMainFn
	defer func() { dispatchAsyncMainFn = origDispatch }()
	dispatchAsyncMainFn = func(fn func()) { fn() }
	origSubtitle := setWindowSubtitle
	defer func() { setWindowSubtitle = origSubtitle }()

	type alertReport struct {
		window appkit.NSWindow
		title  string
		err    error
	}

	var alertMu sync.Mutex
	var alerts []alertReport
	guiAlertPresenter = func(window appkit.NSWindow, title string, err error) {
		alertMu.Lock()
		defer alertMu.Unlock()
		alerts = append(alerts, alertReport{window: window, title: title, err: err})
	}

	var subtitleMu sync.Mutex
	var subtitles []string
	setWindowSubtitle = func(window appkit.NSWindow, subtitle string) {
		subtitleMu.Lock()
		defer subtitleMu.Unlock()
		subtitles = append(subtitles, subtitle)
	}

	tests := []struct {
		name          string
		applyErr      error
		mountErr      error
		folders       []SharedFolderEntry
		wantApply     bool
		wantMount     bool
		wantAlert     bool
		wantAlertMsg  string
		wantSubtitle  string
		checkSequence bool
	}{
		{
			name:      "apply failure stops sequence and reports error",
			applyErr:  errors.New("virtiofs device missing"),
			mountErr:  nil,
			folders:   []SharedFolderEntry{{Path: "/tmp/share", Tag: "share"}},
			wantApply: true,
			wantMount: false,
			wantAlert: true,
		},
		{
			name:      "mount failure reports error",
			applyErr:  nil,
			mountErr:  errors.New("guest mount timeout"),
			folders:   []SharedFolderEntry{{Path: "/tmp/share", Tag: "share"}},
			wantApply: true,
			wantMount: true,
			wantAlert: true,
		},
		{
			name:          "success single folder sets subtitle to tag path",
			applyErr:      nil,
			mountErr:      nil,
			folders:       []SharedFolderEntry{{Path: "/tmp/share", Tag: "share"}},
			wantApply:     true,
			wantMount:     true,
			wantAlert:     false,
			wantSubtitle:  "Shared Folders: /Volumes/My Shared Files/share",
			checkSequence: true,
		},
		{
			name:          "success multiple folders sets subtitle to mount point",
			applyErr:      nil,
			mountErr:      nil,
			folders:       []SharedFolderEntry{{Path: "/tmp/share1", Tag: "s1"}, {Path: "/tmp/share2", Tag: "s2"}},
			wantApply:     true,
			wantMount:     true,
			wantAlert:     false,
			wantSubtitle:  "Shared Folders: /Volumes/My Shared Files",
			checkSequence: true,
		},
		{
			name:      "empty folders clears shares without guest mount",
			applyErr:  nil,
			mountErr:  nil,
			folders:   nil,
			wantApply: true,
			wantMount: false,
			wantAlert: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alertMu.Lock()
			alerts = nil
			alertMu.Unlock()

			subtitleMu.Lock()
			subtitles = nil
			subtitleMu.Unlock()

			tempDir := t.TempDir()
			var seqMu sync.Mutex
			var events []string

			tb := &VMToolbar{
				window:      appkit.NSWindowFromID(objc.ID(123)),
				vmDirectory: tempDir,
				items:       make(map[string]appkit.NSToolbarItem),
			}

			tb.applySharedFoldersFn = func(f []SharedFolderEntry) (int, error) {
				seqMu.Lock()
				events = append(events, "apply")
				seqMu.Unlock()
				if tt.applyErr != nil {
					return 0, tt.applyErr
				}
				return len(f), nil
			}

			tb.mountSharedFoldersFn = func() error {
				seqMu.Lock()
				events = append(events, "mount")
				seqMu.Unlock()
				return tt.mountErr
			}

			done := make(chan struct{})
			tb.saveAndApplySharedFoldersWithDone(tt.folders, done)
			<-done

			seqMu.Lock()
			gotEvents := append([]string(nil), events...)
			seqMu.Unlock()

			applyCalled := false
			mountCalled := false
			for _, ev := range gotEvents {
				if ev == "apply" {
					applyCalled = true
				}
				if ev == "mount" {
					mountCalled = true
				}
			}

			if applyCalled != tt.wantApply {
				t.Errorf("apply called = %v, want %v", applyCalled, tt.wantApply)
			}
			if mountCalled != tt.wantMount {
				t.Errorf("mount called = %v, want %v", mountCalled, tt.wantMount)
			}

			if tt.checkSequence {
				if len(gotEvents) < 2 || gotEvents[0] != "apply" || gotEvents[1] != "mount" {
					t.Fatalf("sequence mismatch: got %v, want [apply mount]", gotEvents)
				}
			}

			alertMu.Lock()
			gotAlerts := append([]alertReport(nil), alerts...)
			alertMu.Unlock()

			if tt.wantAlert {
				if len(gotAlerts) == 0 {
					t.Fatal("expected GUI alert, got none")
				}
				if gotAlerts[0].title != "Shared Folder Error" {
					t.Errorf("alert title = %q, want %q", gotAlerts[0].title, "Shared Folder Error")
				}
				if gotAlerts[0].window.ID != tb.window.ID {
					t.Errorf("alert window.ID = %#x, want %#x", gotAlerts[0].window.ID, tb.window.ID)
				}
			} else if len(gotAlerts) > 0 {
				t.Fatalf("unexpected GUI alert: %v", gotAlerts[0])
			}

			if tt.wantSubtitle != "" {
				subtitleMu.Lock()
				gotSubtitles := append([]string(nil), subtitles...)
				subtitleMu.Unlock()
				if len(gotSubtitles) == 0 || gotSubtitles[len(gotSubtitles)-1] != tt.wantSubtitle {
					t.Errorf("subtitles = %v, want last %q", gotSubtitles, tt.wantSubtitle)
				}
			}
		})
	}
}

func TestToolbarSharedFoldersSaveErrorReportsGUIError(t *testing.T) {
	origPresenter := guiAlertPresenter
	defer func() { guiAlertPresenter = origPresenter }()
	origDispatch := dispatchAsyncMainFn
	defer func() { dispatchAsyncMainFn = origDispatch }()
	dispatchAsyncMainFn = func(fn func()) { fn() }

	var alertMu sync.Mutex
	var alerts []string
	guiAlertPresenter = func(window appkit.NSWindow, title string, err error) {
		alertMu.Lock()
		defer alertMu.Unlock()
		alerts = append(alerts, title)
	}

	tempDir := t.TempDir()
	// Create a regular file where the config file would go so saving fails.
	configPath := filepath.Join(tempDir, "shared_folders.json")
	if err := os.Mkdir(configPath, 0555); err != nil {
		t.Fatal(err)
	}

	tb := &VMToolbar{
		window:      appkit.NSWindowFromID(objc.ID(456)),
		vmDirectory: tempDir,
		items:       make(map[string]appkit.NSToolbarItem),
	}

	done := make(chan struct{})
	tb.saveAndApplySharedFoldersWithDone([]SharedFolderEntry{{Path: "/path", Tag: "t"}}, done)
	<-done

	alertMu.Lock()
	defer alertMu.Unlock()
	if len(alerts) != 1 || alerts[0] != "Shared Folder Error" {
		t.Fatalf("alerts = %v, want [\"Shared Folder Error\"]", alerts)
	}
}
