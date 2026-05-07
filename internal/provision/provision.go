package provision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProvisionConfig configures user provisioning on macOS VMs.
type ProvisionConfig struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	Fullname          string `json:"fullname,omitempty"`
	Admin             bool   `json:"admin"`
	BootstrapRecovery bool   `json:"bootstrap_recovery,omitempty"` // Two-user bootstrap for recovery auth
	InstallXcodeCLI   bool   `json:"install_xcode_cli,omitempty"`  // Optionally install Xcode Command Line Tools
	EnableSSHD        bool   `json:"enable_sshd,omitempty"`        // Enable SSH daemon (Remote Login) on first boot
}

// InjectOptions configures provision file injection behavior.
type InjectOptions struct {
	Config             ProvisionConfig
	SkipSetupAssistant bool
	AutoLogin          bool
	CreateUserPlist    bool // Create user plist directly instead of using LaunchDaemon
	UID                int
	SSHKeyPath         string // Path to SSH public key file for authorized_keys
	InjectAgent        bool   // Cross-compile and inject the vz-agent GRPC daemon
	InjectGuestTools   bool   // Download and inject SPICE guest tools for clipboard sharing
	BootstrapRecovery  bool   // Two-user bootstrap: create hidden admin first, then real user
	EnableSSHD         bool   // Enable SSH daemon (Remote Login) on first boot
	Force              bool   // Re-stage and re-apply even if provisioning already succeeded
}

// ProvisionManifest describes staged provisioning files for the apply phase.
type ProvisionManifest struct {
	Version int                     `json:"version"`
	VMDir   string                  `json:"vmDir"`
	Created time.Time               `json:"created"`
	Files   []ProvisionManifestFile `json:"files"`
}

// ProvisionManifestFile describes a single staged file and its target ownership.
type ProvisionManifestFile struct {
	Path  string `json:"path"`  // relative to Data volume mount point
	Mode  string `json:"mode"`  // octal file mode, e.g. "0755"
	Owner string `json:"owner"` // "root:wheel" or "" for default
}

// StagingFingerprint summarizes the inputs that determined the staged files.
// If two stages produce the same fingerprint, their staged outputs are
// equivalent — so the second can reuse the first instead of re-staging.
type StagingFingerprint struct {
	Username           string `json:"username"`
	Admin              bool   `json:"admin"`
	BootstrapRecovery  bool   `json:"bootstrapRecovery"`
	SkipSetupAssistant bool   `json:"skipSetupAssistant"`
	AutoLogin          bool   `json:"autoLogin"`
	CreateUserPlist    bool   `json:"createUserPlist"`
	InjectAgent        bool   `json:"injectAgent"`
	InjectGuestTools   bool   `json:"injectGuestTools"`
	EnableSSHD         bool   `json:"enableSSHD"`
	SSHKeyPath         string `json:"sshKeyPath,omitempty"`
}

// StageFile writes data to the staging directory and appends a manifest entry.
func StageFile(stagingDir, relativePath string, data []byte, mode os.FileMode, owner string, manifest *ProvisionManifest) error {
	dest := filepath.Join(stagingDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("create staging directory for %s: %w", relativePath, err)
	}
	if err := os.WriteFile(dest, data, mode); err != nil {
		return fmt.Errorf("write staged file %s: %w", relativePath, err)
	}
	manifest.Files = append(manifest.Files, ProvisionManifestFile{
		Path:  relativePath,
		Mode:  fmt.Sprintf("0%o", mode),
		Owner: owner,
	})
	return nil
}

// WriteManifest writes the provision manifest to the staging directory.
func WriteManifest(stagingDir string, manifest *ProvisionManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	return os.WriteFile(filepath.Join(stagingDir, "manifest.json"), data, 0644)
}

// ReadManifest reads a provision manifest from the staging directory.
func ReadManifest(stagingDir string) (*ProvisionManifest, error) {
	data, err := os.ReadFile(filepath.Join(stagingDir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m ProvisionManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// MakeStagingFingerprint captures the values used by stageProvisioningFiles.
func MakeStagingFingerprint(opts InjectOptions) StagingFingerprint {
	return StagingFingerprint{
		Username:           opts.Config.Username,
		Admin:              opts.Config.Admin,
		BootstrapRecovery:  opts.Config.BootstrapRecovery,
		SkipSetupAssistant: opts.SkipSetupAssistant,
		AutoLogin:          opts.AutoLogin,
		CreateUserPlist:    opts.CreateUserPlist,
		InjectAgent:        opts.InjectAgent,
		InjectGuestTools:   opts.InjectGuestTools,
		EnableSSHD:         opts.EnableSSHD,
		SSHKeyPath:         opts.SSHKeyPath,
	}
}

// WriteStagingFingerprint records the fingerprint alongside the manifest so
// later stage-or-reuse decisions can compare without re-deriving it.
func WriteStagingFingerprint(stagingDir string, fp StagingFingerprint) error {
	data, err := json.MarshalIndent(fp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stagingDir, "fingerprint.json"), data, 0644)
}

// ReadStagingFingerprint reads a stored fingerprint from disk.
func ReadStagingFingerprint(stagingDir string) (StagingFingerprint, bool) {
	data, err := os.ReadFile(filepath.Join(stagingDir, "fingerprint.json"))
	if err != nil {
		return StagingFingerprint{}, false
	}
	var fp StagingFingerprint
	if err := json.Unmarshal(data, &fp); err != nil {
		return StagingFingerprint{}, false
	}
	return fp, true
}

// StagingMatchesOptions reports whether stagingDir contains a complete prior
// stage that matches the given options. A matching stage can be reused
// without rebuilding any artifacts.
func StagingMatchesOptions(stagingDir string, opts InjectOptions) (bool, error) {
	if _, err := os.Stat(filepath.Join(stagingDir, "manifest.json")); err != nil {
		return false, err
	}
	data, err := os.ReadFile(filepath.Join(stagingDir, "fingerprint.json"))
	if err != nil {
		return false, err
	}
	var existing StagingFingerprint
	if err := json.Unmarshal(data, &existing); err != nil {
		return false, err
	}
	return existing == MakeStagingFingerprint(opts), nil
}

// ValidateUsername checks if a username is valid for macOS.
func ValidateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if len(username) > 255 {
		return fmt.Errorf("username too long (max 255 characters)")
	}
	// macOS usernames should start with a letter and contain only letters, numbers, and underscores.
	// However, we'll be more permissive and just check for dangerous characters.
	invalidChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|", "\n", "\r", "\t"}
	for _, c := range invalidChars {
		if strings.Contains(username, c) {
			return fmt.Errorf("username contains invalid character: %q", c)
		}
	}
	reserved := []string{"root", "daemon", "nobody", "wheel", "admin", "staff"}
	for _, r := range reserved {
		if strings.ToLower(username) == r {
			return fmt.Errorf("username %q is reserved by the system", username)
		}
	}
	return nil
}

// ShellEscape wraps a value in single quotes with proper escaping for shell scripts.
func ShellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// ManifestNeedsRootProvisioning reports whether a manifest includes any file that must be owned by root:wheel.
func ManifestNeedsRootProvisioning(manifest *ProvisionManifest, autoLoginLaunchDaemonRelativePath string) bool {
	if manifest == nil {
		return false
	}
	for _, file := range manifest.Files {
		switch filepath.Clean(file.Path) {
		case filepath.Join("Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist"),
			filepath.Join("private", "etc", "kcpassword"),
			filepath.Join("Library", "Preferences", "com.apple.loginwindow.plist"),
			filepath.FromSlash(autoLoginLaunchDaemonRelativePath):
			return true
		}
	}
	return false
}

// ManifestIncludesAgent reports whether staged files include a vz-agent component.
func ManifestIncludesAgent(manifest *ProvisionManifest, agentBinaryName, agentLaunchDaemonLabel, agentLaunchAgentLabel string) bool {
	if manifest == nil {
		return false
	}
	for _, file := range manifest.Files {
		switch filepath.Clean(file.Path) {
		case filepath.Join("usr", "local", "bin", agentBinaryName),
			filepath.Join("Library", "LaunchDaemons", agentLaunchDaemonLabel+".plist"),
			filepath.Join("Library", "LaunchAgents", agentLaunchAgentLabel+".plist"):
			return true
		}
	}
	return false
}

// ManifestIncludesLoginScreenCredentials reports whether staged files contain both
// login screen credential artifacts.
func ManifestIncludesLoginScreenCredentials(manifest *ProvisionManifest) bool {
	if manifest == nil {
		return false
	}
	var hasKCPassword, hasLoginWindow bool
	for _, file := range manifest.Files {
		switch filepath.Clean(file.Path) {
		case filepath.Join("private", "etc", "kcpassword"):
			hasKCPassword = true
		case filepath.Join("Library", "Preferences", "com.apple.loginwindow.plist"):
			hasLoginWindow = true
		}
	}
	return hasKCPassword && hasLoginWindow
}

// RootWheelVerifyTargets returns staged file targets that require root:wheel ownership.
func RootWheelVerifyTargets(manifest *ProvisionManifest, mountPoint string) []string {
	if manifest == nil {
		return nil
	}
	var targets []string
	for _, f := range manifest.Files {
		if f.Owner == "root:wheel" {
			targets = append(targets, filepath.Join(mountPoint, f.Path))
		}
	}
	return targets
}
