// provision_inject.go - File staging and injection logic for VM provisioning.
//
// This file consolidates the various provisioning file operations:
//   - LaunchDaemon plist and provisioning script generation
//   - Auto-login configuration (kcpassword + loginwindow.plist)
//   - User plist creation and admin group management
//   - SSH key injection
//
// Each operation has two variants: an "inject" function that writes directly
// to a mounted disk, and a "stage" function that writes to a staging directory
// for later application via applyStagedFiles.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pw "github.com/tmc/cove/internal/password"
)

const (
	autoLoginScriptRelativePath       = "private/var/db/vz-autologin.sh"
	autoLoginLaunchDaemonRelativePath = "Library/LaunchDaemons/com.github.tmc.vz-macos.autologin.plist"
)

// --- LaunchDaemon provisioning ---

// stageLaunchDaemonProvisioning stages the LaunchDaemon plist and provision
// script to the staging directory.
func stageLaunchDaemonProvisioning(stagingDir string, config ProvisionConfig, manifest *ProvisionManifest) error {
	scriptContent, err := generateEmbeddedProvisionScript(config)
	if err != nil {
		return fmt.Errorf("generate provision script: %w", err)
	}
	if err := stageFile(stagingDir, filepath.Join("private", "var", "db", "vz-provision.sh"),
		[]byte(scriptContent), 0755, "root:wheel", manifest); err != nil {
		return err
	}

	plistContent := generateEmbeddedLaunchDaemonPlist()
	if err := stageFile(stagingDir, filepath.Join("Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist"),
		[]byte(plistContent), 0644, "root:wheel", manifest); err != nil {
		return err
	}
	return nil
}

// generateEmbeddedLaunchDaemonPlist returns a LaunchDaemon plist that references
// the self-contained script at /var/db/vz-provision.sh (no VirtioFS dependency)
func generateEmbeddedLaunchDaemonPlist() string {
	return provisionLaunchDaemonPlist
}

// generateEmbeddedProvisionScript returns a self-contained provisioning script
// with all configuration embedded (no external file dependencies)
func generateEmbeddedProvisionScript(config ProvisionConfig) (string, error) {
	fullname := config.Fullname
	if fullname == "" {
		fullname = config.Username
	}
	boolStr := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	data := provisionTemplateData{
		Username:          shellEscape(config.Username),
		Password:          shellEscape(config.Password),
		Fullname:          shellEscape(fullname),
		Admin:             boolStr(config.Admin),
		BootstrapRecovery: boolStr(config.BootstrapRecovery),
		InstallXcodeCLI:   boolStr(config.InstallXcodeCLI),
		EnableSSHD:        boolStr(config.EnableSSHD),
	}
	result, err := renderProvisionScript(data)
	if err != nil {
		return "", fmt.Errorf("render provision script: %w", err)
	}
	return result, nil
}

func generateEmbeddedAutoLoginScript(username string) (string, error) {
	result, err := renderAutoLoginScript(autoLoginTemplateData{
		Username: shellEscape(username),
	})
	if err != nil {
		return "", fmt.Errorf("render autologin script: %w", err)
	}
	return result, nil
}

// --- Auto-login injection ---

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
	encodedPassword := pw.EncodeKC(password)
	if err := os.WriteFile(kcpasswordPath, encodedPassword, 0600); err != nil {
		return fmt.Errorf("write kcpassword: %w", err)
	}
	chownRootWheel(kcpasswordPath, rootFiles)
	if err := validateKCPasswordFile(kcpasswordPath, password); err != nil {
		return err
	}
	fmt.Printf("Written: %s\n", kcpasswordPath)

	// Create loginwindow.plist
	prefsDir := filepath.Join(mountPoint, "Library", "Preferences")
	if err := os.MkdirAll(prefsDir, 0755); err != nil {
		return fmt.Errorf("create preferences directory: %w", err)
	}

	loginWindowPlist := pw.CreateLoginWindowPlist(username)
	plistData, err := pw.EncodeLoginWindowPlist(loginWindowPlist)
	if err != nil {
		return fmt.Errorf("encode loginwindow plist: %w", err)
	}

	loginWindowPath := filepath.Join(prefsDir, "com.apple.loginwindow.plist")
	if err := os.WriteFile(loginWindowPath, plistData, 0644); err != nil {
		return fmt.Errorf("write loginwindow plist: %w", err)
	}
	chownRootWheel(loginWindowPath, rootFiles)
	fmt.Printf("Written: %s\n", loginWindowPath)

	autoLoginScript, err := generateEmbeddedAutoLoginScript(username)
	if err != nil {
		return err
	}
	autoLoginScriptPath := filepath.Join(mountPoint, filepath.FromSlash(autoLoginScriptRelativePath))
	if err := os.MkdirAll(filepath.Dir(autoLoginScriptPath), 0755); err != nil {
		return fmt.Errorf("create autologin script directory: %w", err)
	}
	if err := os.WriteFile(autoLoginScriptPath, []byte(autoLoginScript), 0755); err != nil {
		return fmt.Errorf("write autologin script: %w", err)
	}
	chownRootWheel(autoLoginScriptPath, rootFiles)
	fmt.Printf("Written: %s\n", autoLoginScriptPath)

	autoLoginPlistPath := filepath.Join(mountPoint, filepath.FromSlash(autoLoginLaunchDaemonRelativePath))
	if err := os.MkdirAll(filepath.Dir(autoLoginPlistPath), 0755); err != nil {
		return fmt.Errorf("create autologin LaunchDaemon directory: %w", err)
	}
	if err := os.WriteFile(autoLoginPlistPath, []byte(autoLoginLaunchDaemonPlist), 0644); err != nil {
		return fmt.Errorf("write autologin LaunchDaemon plist: %w", err)
	}
	chownRootWheel(autoLoginPlistPath, rootFiles)
	fmt.Printf("Written: %s\n", autoLoginPlistPath)

	return nil
}

// stageAutoLogin stages kcpassword and loginwindow.plist to the staging directory.
func stageAutoLogin(stagingDir, username, password string, manifest *ProvisionManifest) error {
	encodedPassword := pw.EncodeKC(password)
	if err := stageFile(stagingDir, filepath.Join("private", "etc", "kcpassword"),
		encodedPassword, 0600, "root:wheel", manifest); err != nil {
		return err
	}
	if err := validateKCPasswordFile(filepath.Join(stagingDir, "private", "etc", "kcpassword"), password); err != nil {
		return err
	}

	loginWindowPlist := pw.CreateLoginWindowPlist(username)
	plistData, err := pw.EncodeLoginWindowPlist(loginWindowPlist)
	if err != nil {
		return fmt.Errorf("encode loginwindow plist: %w", err)
	}
	if err := stageFile(stagingDir, filepath.Join("Library", "Preferences", "com.apple.loginwindow.plist"),
		plistData, 0644, "root:wheel", manifest); err != nil {
		return err
	}
	autoLoginScript, err := generateEmbeddedAutoLoginScript(username)
	if err != nil {
		return err
	}
	if err := stageFile(stagingDir, filepath.FromSlash(autoLoginScriptRelativePath),
		[]byte(autoLoginScript), 0755, "root:wheel", manifest); err != nil {
		return err
	}
	if err := stageFile(stagingDir, filepath.FromSlash(autoLoginLaunchDaemonRelativePath),
		[]byte(autoLoginLaunchDaemonPlist), 0644, "root:wheel", manifest); err != nil {
		return err
	}
	return nil
}

func validateKCPasswordFile(path, password string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read kcpassword: %w", err)
	}
	if want := pw.EncodeKC(password); !bytes.Equal(data, want) {
		return fmt.Errorf("validate kcpassword: encoded bytes do not match password")
	}
	if got := pw.DecodeKC(data); got != password {
		return fmt.Errorf("validate kcpassword: decoded password mismatch")
	}
	return nil
}

// --- User plist injection ---

// stageUserPlist stages the direct user plist to the staging directory.
func stageUserPlist(stagingDir string, opts InjectOptions, manifest *ProvisionManifest) error {
	config := opts.Config

	userPlist, err := pw.CreateUserPlist(config.Username, config.Fullname, config.Password, opts.UID, config.Admin)
	if err != nil {
		return fmt.Errorf("create user plist: %w", err)
	}
	plistData, err := pw.EncodeUserPlist(userPlist)
	if err != nil {
		return fmt.Errorf("encode user plist: %w", err)
	}

	if err := stageFile(stagingDir,
		filepath.Join("private", "var", "db", "dslocal", "nodes", "Default", "users", config.Username+".plist"),
		plistData, 0600, "", manifest); err != nil {
		return err
	}

	// Note: home directory creation and admin group modification are not staged
	// because they may need to interact with existing data on the disk.
	// The apply phase handles these or the LaunchDaemon creates them at boot.
	return nil
}

// --- SSH key injection ---

// stageSSHKey stages the SSH public key to the staging directory.
func stageSSHKey(stagingDir, username, sshKeyPath string, manifest *ProvisionManifest) error {
	keyData, err := os.ReadFile(sshKeyPath)
	if err != nil {
		return fmt.Errorf("read SSH key file: %w", err)
	}
	keyStr := strings.TrimSpace(string(keyData))
	if !strings.HasPrefix(keyStr, "ssh-") && !strings.HasPrefix(keyStr, "ecdsa-") && !strings.HasPrefix(keyStr, "sk-") {
		return fmt.Errorf("file does not appear to be an SSH public key")
	}
	return stageFile(stagingDir, filepath.Join("Users", username, ".ssh", "authorized_keys"),
		[]byte(keyStr+"\n"), 0600, "", manifest)
}
