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

var powerboxPromptDirectory = promptPowerboxDirectoryNative

func withPowerboxFallback(action func() error) error {
	err := action()
	if err == nil {
		return nil
	}
	var grantRequired *powerboxGrantRequiredError
	if !errors.As(err, &grantRequired) {
		return err
	}
	grant, err := powerboxPromptDirectory(powerboxPromptTitle(grantRequired), powerboxPromptMessage(grantRequired))
	if err != nil {
		return err
	}
	if _, err := saveSecurityBookmarkBytes(grantRequired.StorePath, grantRequired.Key, grantRequired.Kind, grant.Path, grant.Bookmark); err != nil {
		return fmt.Errorf("save Powerbox bookmark: %w", err)
	}
	return action()
}

func powerboxPromptTitle(grant *powerboxGrantRequiredError) string {
	if grant.Action != "" {
		return "Grant cove access: " + grant.Action
	}
	return "Grant cove access"
}

func powerboxPromptMessage(grant *powerboxGrantRequiredError) string {
	if grant.Key != "" {
		return "Choose the directory to grant for " + grant.Key + "."
	}
	return "Choose a directory to grant to cove."
}

func promptPowerboxDirectoryNative(title, message string) (powerboxDirectoryGrant, error) {
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
