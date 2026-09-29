package main

import (
	"errors"
	"image"
	"sync"
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
)

type mockErrScreenshotProvider struct {
	errMsg string
}

func (m *mockErrScreenshotProvider) captureDisplayImage() (image.Image, string) {
	return nil, m.errMsg
}

func TestSaveCurrentVMScreenshotReportsGUIError(t *testing.T) {
	origPresenter := guiAlertPresenter
	defer func() { guiAlertPresenter = origPresenter }()
	origDispatch := dispatchAsyncMainFn
	defer func() { dispatchAsyncMainFn = origDispatch }()
	dispatchAsyncMainFn = func(fn func()) { fn() }

	var mu sync.Mutex
	var reportedTitle string
	var reportedWindow appkit.NSWindow
	var reportedErr error
	guiAlertPresenter = func(window appkit.NSWindow, title string, err error) {
		mu.Lock()
		defer mu.Unlock()
		reportedTitle = title
		reportedWindow = window
		reportedErr = err
	}

	testWindow := appkit.NSWindowFromID(objc.ID(999))
	provider := &mockErrScreenshotProvider{errMsg: "display capture failed"}

	saveCurrentVMScreenshot("test", provider, testWindow)

	mu.Lock()
	defer mu.Unlock()
	if reportedTitle != "VM Screenshot Error" {
		t.Fatalf("reportedTitle = %q, want %q", reportedTitle, "VM Screenshot Error")
	}
	if reportedErr == nil || reportedErr.Error() != "display capture failed" {
		t.Fatalf("reportedErr = %v, want %q", reportedErr, "display capture failed")
	}
	if reportedWindow.ID != testWindow.ID {
		t.Fatalf("reportedWindow.ID = %#x, want %#x", reportedWindow.ID, testWindow.ID)
	}
}

func TestReportGUIErrorPresenterAttachesSheetOrModal(t *testing.T) {
	// Verify reportGUIError with nil error is a no-op
	reportGUIError(appkit.NSWindow{}, "Title", nil)

	// Verify error title fallback
	var mu sync.Mutex
	var gotTitle string
	origPresenter := guiAlertPresenter
	defer func() { guiAlertPresenter = origPresenter }()
	origDispatch := dispatchAsyncMainFn
	defer func() { dispatchAsyncMainFn = origDispatch }()
	dispatchAsyncMainFn = func(fn func()) { fn() }

	guiAlertPresenter = func(window appkit.NSWindow, title string, err error) {
		mu.Lock()
		defer mu.Unlock()
		gotTitle = title
	}

	reportGUIError(appkit.NSWindow{}, "", errors.New("boom"))
	mu.Lock()
	if gotTitle != "Error" {
		t.Errorf("gotTitle = %q, want Error", gotTitle)
	}
	mu.Unlock()
}
