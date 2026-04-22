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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/x/plist"
)

const (
	autoLoginScriptRelativePath       = "private/var/db/vz-autologin.sh"
	autoLoginLaunchDaemonRelativePath = "Library/LaunchDaemons/com.github.tmc.vz-macos.autologin.plist"
)

// --- Username validation ---

// validateUsername checks if a username is valid for macOS
func validateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if len(username) > 255 {
		return fmt.Errorf("username too long (max 255 characters)")
	}
	// macOS usernames should start with a letter and contain only letters, numbers, and underscores
	// However, we'll be more permissive and just check for dangerous characters
	invalidChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\n", "\r", "\t"}
	for _, c := range invalidChars {
		if strings.Contains(username, c) {
			return fmt.Errorf("username contains invalid character: %q", c)
		}
	}
	// Check for reserved usernames
	reserved := []string{"root", "daemon", "nobody", "wheel", "admin", "staff"}
	for _, r := range reserved {
		if strings.ToLower(username) == r {
			return fmt.Errorf("username %q is reserved by the system", username)
		}
	}
	return nil
}

// --- LaunchDaemon provisioning ---

// shellEscape wraps a value in single quotes with proper escaping for safe
// embedding in shell scripts. Single quotes within the value are replaced
// with the sequence '\" (end quote, escaped quote, start quote).
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// injectLaunchDaemonProvisioning creates the LaunchDaemon and script for first-boot provisioning.
// Files needing root:wheel ownership are collected in rootFiles for a later targeted sudo chown.
func injectLaunchDaemonProvisioning(mountPoint string, config ProvisionConfig, rootFiles *[]string) error {
	// Write the self-contained provisioning script
	// On the Data volume, /var/db is at private/var/db
	scriptDir := filepath.Join(mountPoint, "private", "var", "db")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		return fmt.Errorf("create script directory: %w", err)
	}

	scriptPath := filepath.Join(scriptDir, "vz-provision.sh")
	scriptContent, err := generateEmbeddedProvisionScript(config)
	if err != nil {
		return fmt.Errorf("generate provision script: %w", err)
	}
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		return fmt.Errorf("write provision script: %w", err)
	}
	chownRootWheel(scriptPath, rootFiles)
	fmt.Printf("Written: %s\n", scriptPath)

	// Write the LaunchDaemon plist
	// On the Data volume, /Library/LaunchDaemons is at Library/LaunchDaemons
	launchDaemonsDir := filepath.Join(mountPoint, "Library", "LaunchDaemons")
	if err := os.MkdirAll(launchDaemonsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchDaemons directory: %w", err)
	}

	plistPath := filepath.Join(launchDaemonsDir, "com.github.tmc.vz-macos.provision.plist")
	plistContent := generateEmbeddedLaunchDaemonPlist()
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("write LaunchDaemon plist: %w", err)
	}
	chownRootWheel(plistPath, rootFiles)
	fmt.Printf("Written: %s\n", plistPath)

	return nil
}

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

// --- User plist injection ---

// injectUserPlist creates a user plist directly with password hash (advanced mode)
func injectUserPlist(mountPoint string, opts InjectOptions) error {
	config := opts.Config

	// Create the user plist with proper password hash
	userPlist, err := CreateUserPlist(config.Username, config.Fullname, config.Password, opts.UID, config.Admin)
	if err != nil {
		return fmt.Errorf("create user plist: %w", err)
	}

	// Encode to binary plist format
	plistData, err := EncodeUserPlist(userPlist)
	if err != nil {
		return fmt.Errorf("encode user plist: %w", err)
	}

	// Write user plist to /var/db/dslocal/nodes/Default/users/<username>.plist
	usersDir := filepath.Join(mountPoint, "private", "var", "db", "dslocal", "nodes", "Default", "users")
	if err := os.MkdirAll(usersDir, 0700); err != nil {
		return fmt.Errorf("create users directory: %w", err)
	}

	userPlistPath := filepath.Join(usersDir, config.Username+".plist")
	if err := os.WriteFile(userPlistPath, plistData, 0600); err != nil {
		return fmt.Errorf("write user plist: %w", err)
	}
	fmt.Printf("Written: %s\n", userPlistPath)

	// Create home directory structure
	homeDir := filepath.Join(mountPoint, "Users", config.Username)
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		return fmt.Errorf("create home directory: %w", err)
	}

	// Create standard subdirectories
	if err := CreateHomeDirectoryStructure(homeDir, opts.UID, 20); err != nil {
		fmt.Printf("warning: home directory structure incomplete: %v\n", err)
	}
	fmt.Printf("Created home directory: %s\n", homeDir)

	// Add user to admin group if requested
	if config.Admin {
		if err := addUserToAdminGroup(mountPoint, config.Username); err != nil {
			fmt.Printf("warning: could not add to admin group: %v\n", err)
		}
	}

	return nil
}

// addUserToAdminGroup adds the user to the admin group plist
func addUserToAdminGroup(mountPoint, username string) error {
	// Admin group is stored at /var/db/dslocal/nodes/Default/groups/admin.plist
	groupsDir := filepath.Join(mountPoint, "private", "var", "db", "dslocal", "nodes", "Default", "groups")
	adminPlistPath := filepath.Join(groupsDir, "admin.plist")

	// Read existing admin.plist if it exists
	var groupData map[string]interface{}
	if data, err := os.ReadFile(adminPlistPath); err == nil {
		if _, err := plist.Unmarshal(data, &groupData); err != nil {
			// If we can't parse existing file, create new
			groupData = nil
		}
	}

	// Create or update group plist
	if groupData == nil {
		groupData = map[string]interface{}{
			"name":         []string{"admin"},
			"gid":          []string{"80"},
			"users":        []string{username},
			"groupmembers": []string{},
		}
	} else {
		// Add username to users array if not present
		users, ok := groupData["users"].([]interface{})
		if !ok {
			users = []interface{}{}
		}
		found := false
		for _, u := range users {
			if u == username {
				found = true
				break
			}
		}
		if !found {
			users = append(users, username)
			groupData["users"] = users
		}
	}

	// Write updated admin.plist
	if err := os.MkdirAll(groupsDir, 0700); err != nil {
		return fmt.Errorf("create groups directory: %w", err)
	}

	data, err := plist.Marshal(groupData, plist.FormatBinary)
	if err != nil {
		return fmt.Errorf("marshal admin plist: %w", err)
	}

	if err := os.WriteFile(adminPlistPath, data, 0600); err != nil {
		return fmt.Errorf("write admin plist: %w", err)
	}
	fmt.Printf("Updated admin group: %s\n", adminPlistPath)

	return nil
}

// stageUserPlist stages the direct user plist to the staging directory.
func stageUserPlist(stagingDir string, opts InjectOptions, manifest *ProvisionManifest) error {
	config := opts.Config

	userPlist, err := CreateUserPlist(config.Username, config.Fullname, config.Password, opts.UID, config.Admin)
	if err != nil {
		return fmt.Errorf("create user plist: %w", err)
	}
	plistData, err := EncodeUserPlist(userPlist)
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

// injectSSHKey creates the .ssh directory and authorized_keys file for a user
func injectSSHKey(mountPoint, username, sshKeyPath string) error {
	fmt.Println("Configuring SSH key...")

	// Read the SSH public key file
	keyData, err := os.ReadFile(sshKeyPath)
	if err != nil {
		return fmt.Errorf("read SSH key file: %w", err)
	}

	// Validate it looks like an SSH public key
	keyStr := strings.TrimSpace(string(keyData))
	if !strings.HasPrefix(keyStr, "ssh-") && !strings.HasPrefix(keyStr, "ecdsa-") && !strings.HasPrefix(keyStr, "sk-") {
		return fmt.Errorf("file does not appear to be an SSH public key (expected ssh-*, ecdsa-*, or sk-* prefix)")
	}

	// Create the user's .ssh directory
	// On the Data volume, user homes are at /Users/<username>
	sshDir := filepath.Join(mountPoint, "Users", username, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("create .ssh directory: %w", err)
	}

	// Write the authorized_keys file
	authorizedKeysPath := filepath.Join(sshDir, "authorized_keys")
	if err := os.WriteFile(authorizedKeysPath, []byte(keyStr+"\n"), 0600); err != nil {
		return fmt.Errorf("write authorized_keys: %w", err)
	}
	fmt.Printf("Written: %s\n", authorizedKeysPath)

	// Note: The ownership will need to be fixed by the provisioning script
	// or on first login, since we can't set UID/GID when writing from the host
	fmt.Printf("Note: SSH key added. Ownership will be set to %s on first login.\n", username)

	return nil
}

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
