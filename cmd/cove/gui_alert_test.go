package main

import (
	"errors"
	"sync"
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
)

func TestReportGUIError(t *testing.T) {
	origPresenter := guiAlertPresenter
	defer func() { guiAlertPresenter = origPresenter }()
	origDispatch := dispatchAsyncMainFn
	defer func() { dispatchAsyncMainFn = origDispatch }()
	dispatchAsyncMainFn = func(fn func()) { fn() }

	type alertCall struct {
		window appkit.NSWindow
		title  string
		err    error
	}

	var mu sync.Mutex
	var calls []alertCall
	guiAlertPresenter = func(window appkit.NSWindow, title string, err error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, alertCall{window: window, title: title, err: err})
	}

	tests := []struct {
		name      string
		window    appkit.NSWindow
		title     string
		err       error
		wantCalls int
		wantTitle string
	}{
		{
			name:      "nil error does not report",
			window:    appkit.NSWindow{},
			title:     "Title",
			err:       nil,
			wantCalls: 0,
		},
		{
			name:      "reports error with default title if empty",
			window:    appkit.NSWindow{},
			title:     "",
			err:       errors.New("something went wrong"),
			wantCalls: 1,
			wantTitle: "Error",
		},
		{
			name:      "reports error with specified title and window",
			window:    appkit.NSWindowFromID(objc.ID(42)),
			title:     "VM Stop Error",
			err:       errors.New("stop failed"),
			wantCalls: 1,
			wantTitle: "VM Stop Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mu.Lock()
			calls = nil
			mu.Unlock()

			reportGUIError(tt.window, tt.title, tt.err)

			mu.Lock()
			defer mu.Unlock()
			if len(calls) != tt.wantCalls {
				t.Fatalf("got %d calls, want %d", len(calls), tt.wantCalls)
			}
			if tt.wantCalls > 0 {
				got := calls[0]
				if got.title != tt.wantTitle {
					t.Errorf("title = %q, want %q", got.title, tt.wantTitle)
				}
				if got.err.Error() != tt.err.Error() {
					t.Errorf("err = %v, want %v", got.err, tt.err)
				}
				if got.window.ID != tt.window.ID {
					t.Errorf("window.ID = %#x, want %#x", got.window.ID, tt.window.ID)
				}
			}
		})
	}
}
