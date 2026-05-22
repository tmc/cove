package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/x/plist"
	"github.com/tmc/cove/internal/password"
)

type loginScreenCredentials struct {
	Username string
	Password string
}

const loginScreenCredentialsFile = "autologin.json"

func (c loginScreenCredentials) Valid() bool {
	return c.Username != "" && c.Password != ""
}

var bootLoginScreenCredentials loginScreenCredentials

func loginScreenCredentialsPath(vmDir string) string {
	return filepath.Join(vmDir, loginScreenCredentialsFile)
}

func readLoginScreenCredentialsCache(vmDir string) (loginScreenCredentials, error) {
	data, err := os.ReadFile(loginScreenCredentialsPath(vmDir))
	if err != nil {
		if os.IsNotExist(err) {
			return loginScreenCredentials{}, nil
		}
		return loginScreenCredentials{}, fmt.Errorf("read login credential cache: %w", err)
	}
	var creds loginScreenCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return loginScreenCredentials{}, fmt.Errorf("parse login credential cache: %w", err)
	}
	if !creds.Valid() {
		return loginScreenCredentials{}, nil
	}
	return creds, nil
}

func writeLoginScreenCredentialsCache(vmDir string, creds loginScreenCredentials) error {
	if !creds.Valid() {
		return nil
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("marshal login credential cache: %w", err)
	}
	path := loginScreenCredentialsPath(vmDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write login credential cache: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename login credential cache: %w", err)
	}
	return nil
}

func readLoginScreenCredentials(root string) (loginScreenCredentials, error) {
	kcData, err := os.ReadFile(filepath.Join(root, "private", "etc", "kcpassword"))
	if err != nil {
		if os.IsNotExist(err) {
			return loginScreenCredentials{}, nil
		}
		return loginScreenCredentials{}, fmt.Errorf("read kcpassword: %w", err)
	}

	loginWindowData, err := os.ReadFile(filepath.Join(root, "Library", "Preferences", "com.apple.loginwindow.plist"))
	if err != nil {
		if os.IsNotExist(err) {
			return loginScreenCredentials{}, nil
		}
		return loginScreenCredentials{}, fmt.Errorf("read loginwindow plist: %w", err)
	}

	var prefs password.LoginWindowPlist
	if _, err := plist.Unmarshal(loginWindowData, &prefs); err != nil {
		return loginScreenCredentials{}, fmt.Errorf("parse loginwindow plist: %w", err)
	}

	creds := loginScreenCredentials{
		Username: strings.TrimSpace(prefs.AutoLoginUser),
		Password: password.DecodeKC(kcData),
	}
	if !creds.Valid() {
		return loginScreenCredentials{}, nil
	}
	return creds, nil
}

func loadBootLoginScreenCredentials(vmDir, diskPath string) (loginScreenCredentials, error) {
	// Do not mount diskPath here. This runs on the VM boot path, just before
	// Virtualization.framework opens the disk, so the watchdog must rely on
	// the host-side cache written during provisioning.
	creds, err := readLoginScreenCredentialsCache(vmDir)
	if err != nil {
		return loginScreenCredentials{}, err
	}
	if creds.Valid() {
		return creds, nil
	}
	return loginScreenCredentials{}, nil
}

func resolveLoginScreenWatchdogCredentials() loginScreenCredentials {
	if provisionUser != "" && provisionPassword != "" && didInjectSucceed() {
		return loginScreenCredentials{
			Username: provisionUser,
			Password: provisionPassword,
		}
	}
	return bootLoginScreenCredentials
}
