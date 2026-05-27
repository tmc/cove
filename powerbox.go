//go:build darwin

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/uniformtypeidentifiers"
)

var errPowerboxCanceled = errors.New("powerbox selection canceled")

type powerboxDirectoryGrant struct {
	Path     string
	Bookmark []byte
}

type powerboxFileGrant struct {
	Path     string
	Bookmark []byte
}

var powerboxPromptDirectory = promptPowerboxDirectoryNative
var powerboxPromptFile = promptPowerboxFileNative

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
	panel, err := newPowerboxOpenPanel(title, message)
	if err != nil {
		return powerboxDirectoryGrant{}, err
	}
	panel.SetCanChooseFiles(false)
	panel.SetCanChooseDirectories(true)
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

func promptPowerboxFileNative(title, message string, extensions []string) (powerboxFileGrant, error) {
	panel, err := newPowerboxOpenPanel(title, message)
	if err != nil {
		return powerboxFileGrant{}, err
	}
	panel.SetCanChooseFiles(true)
	panel.SetCanChooseDirectories(false)
	if len(extensions) > 0 {
		panel.SetAllowedContentTypes(powerboxAllowedContentTypes(extensions))
	}
	if resp := panel.RunModal(); resp != appkit.NSModalResponse(1) {
		return powerboxFileGrant{}, errPowerboxCanceled
	}
	return powerboxFileGrantForURL(panel.URL(), extensions)
}

func newPowerboxOpenPanel(title, message string) (appkit.NSOpenPanel, error) {
	panel := appkit.GetNSOpenPanelClass().OpenPanel()
	if panel.GetID() == 0 {
		return appkit.NSOpenPanel{}, fmt.Errorf("create Powerbox open panel: nil NSOpenPanel")
	}
	panel.SetAllowsMultipleSelection(false)
	panel.SetResolvesAliases(true)
	panel.SetPrompt("Open")
	if title != "" {
		panel.SetTitle(title)
	}
	if message != "" {
		panel.SetMessage(message)
	}
	return panel, nil
}

func powerboxFileGrantForURL(url foundation.NSURL, extensions []string) (powerboxFileGrant, error) {
	if url.GetID() == 0 {
		return powerboxFileGrant{}, fmt.Errorf("Powerbox returned nil URL")
	}
	path := url.Path()
	if !powerboxFileExtensionAllowed(path, extensions) {
		return powerboxFileGrant{}, fmt.Errorf("Powerbox selected unsupported file type: %s", path)
	}
	bookmark, err := createSecurityScopedBookmarkForURL(url)
	if err != nil {
		return powerboxFileGrant{}, err
	}
	return powerboxFileGrant{
		Path:     path,
		Bookmark: bookmark,
	}, nil
}

func powerboxAllowedContentTypes(extensions []string) []uniformtypeidentifiers.UTType {
	types := make([]uniformtypeidentifiers.UTType, 0, len(extensions))
	for _, ext := range extensions {
		ext = strings.TrimPrefix(strings.TrimSpace(ext), ".")
		if ext == "" {
			continue
		}
		t := uniformtypeidentifiers.NewTypeWithFilenameExtension(ext)
		if t.GetID() != 0 {
			types = append(types, t)
		}
	}
	return types
}

func powerboxFileExtensionAllowed(path string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	got := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	for _, ext := range extensions {
		if got == strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".") {
			return true
		}
	}
	return false
}
