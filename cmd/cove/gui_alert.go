package main

import (
	"github.com/tmc/apple/appkit"
)

var guiAlertPresenter = func(window appkit.NSWindow, title string, err error) {
	alert := appkit.NewNSAlert()
	alert.SetAlertStyle(appkit.NSAlertStyleCritical)
	alert.SetMessageText(title)
	alert.SetInformativeText(err.Error())
	alert.AddButtonWithTitle("OK")
	if window.ID != 0 {
		alert.BeginSheetModalForWindowCompletionHandler(window, nil)
	} else {
		alert.RunModal()
	}
}

// reportGUIError displays an error alert to the user.
// If window is non-zero, it presents as a sheet modal on the window.
// Otherwise, it runs modally.
// It always dispatches to the main thread via DispatchAsyncMain.
func reportGUIError(window appkit.NSWindow, title string, err error) {
	if err == nil {
		return
	}
	if title == "" {
		title = "Error"
	}
	DispatchAsyncMain(func() {
		guiAlertPresenter(window, title, err)
	})
}
