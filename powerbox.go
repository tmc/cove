//go:build darwin

package main

import (
	"errors"
	"fmt"

	"github.com/tmc/apple/appkit"
)

var errPowerboxCanceled = errors.New("powerbox selection canceled")

type powerboxDirectoryGrant struct {
	Path     string
	Bookmark []byte
}

func promptPowerboxDirectory(title, message string) (powerboxDirectoryGrant, error) {
	panel := appkit.GetNSOpenPanelClass().OpenPanel()
	if panel.GetID() == 0 {
		return powerboxDirectoryGrant{}, fmt.Errorf("create Powerbox open panel: nil NSOpenPanel")
	}
	panel.SetCanChooseFiles(false)
	panel.SetCanChooseDirectories(true)
	panel.SetAllowsMultipleSelection(false)
	panel.SetResolvesAliases(true)
	panel.SetPrompt("Open")
	if title != "" {
		panel.SetTitle(title)
	}
	if message != "" {
		panel.SetMessage(message)
	}
	if resp := panel.RunModal(); resp != appkit.NSModalResponse(1) {
		return powerboxDirectoryGrant{}, errPowerboxCanceled
	}
	url := panel.URL()
	if url.GetID() == 0 {
		return powerboxDirectoryGrant{}, fmt.Errorf("Powerbox returned nil URL")
	}
	bookmark, err := createSecurityScopedBookmarkForURL(url)
	if err != nil {
		return powerboxDirectoryGrant{}, err
	}
	return powerboxDirectoryGrant{
		Path:     url.Path(),
		Bookmark: bookmark,
	}, nil
}
