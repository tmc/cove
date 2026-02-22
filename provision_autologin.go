package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// injectAutoLogin configures automatic login by creating kcpassword and loginwindow.plist.
// Files needing root:wheel ownership are collected in rootFiles for a later targeted sudo chown.
func injectAutoLogin(mountPoint, username, password string, rootFiles *[]string) error {
	fmt.Println("Configuring auto-login...")

	// Create /etc/kcpassword with encoded password
	etcDir := filepath.Join(mountPoint, "private", "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		return fmt.Errorf("create etc directory: %w", err)
	}

	kcpasswordPath := filepath.Join(etcDir, "kcpassword")
	encodedPassword := EncodeKCPassword(password)
	if err := os.WriteFile(kcpasswordPath, encodedPassword, 0600); err != nil {
		return fmt.Errorf("write kcpassword: %w", err)
	}
	chownRootWheel(kcpasswordPath, rootFiles)
	fmt.Printf("Written: %s\n", kcpasswordPath)

	// Create loginwindow.plist
	prefsDir := filepath.Join(mountPoint, "Library", "Preferences")
	if err := os.MkdirAll(prefsDir, 0755); err != nil {
		return fmt.Errorf("create preferences directory: %w", err)
	}

	loginWindowPlist := CreateLoginWindowPlist(username)
	plistData, err := EncodeLoginWindowPlist(loginWindowPlist)
	if err != nil {
		return fmt.Errorf("encode loginwindow plist: %w", err)
	}

	loginWindowPath := filepath.Join(prefsDir, "com.apple.loginwindow.plist")
	if err := os.WriteFile(loginWindowPath, plistData, 0644); err != nil {
		return fmt.Errorf("write loginwindow plist: %w", err)
	}
	chownRootWheel(loginWindowPath, rootFiles)
	fmt.Printf("Written: %s\n", loginWindowPath)

	return nil
}

// stageAutoLogin stages kcpassword and loginwindow.plist to the staging directory.
func stageAutoLogin(stagingDir, username, password string, manifest *ProvisionManifest) error {
	encodedPassword := EncodeKCPassword(password)
	if err := stageFile(stagingDir, filepath.Join("private", "etc", "kcpassword"),
		encodedPassword, 0600, "root:wheel", manifest); err != nil {
		return err
	}

	loginWindowPlist := CreateLoginWindowPlist(username)
	plistData, err := EncodeLoginWindowPlist(loginWindowPlist)
	if err != nil {
		return fmt.Errorf("encode loginwindow plist: %w", err)
	}
	if err := stageFile(stagingDir, filepath.Join("Library", "Preferences", "com.apple.loginwindow.plist"),
		plistData, 0644, "root:wheel", manifest); err != nil {
		return err
	}
	return nil
}
