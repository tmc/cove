package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// SandboxLevel selects an isolation policy for a VM run.
type SandboxLevel string

const (
	// SandboxLevelUnset means no sandbox policy is applied.
	SandboxLevelUnset SandboxLevel = ""
	// SandboxLevelMinimal keeps guest communication available but blocks host
	// sharing defaults and saved host-sharing state.
	SandboxLevelMinimal SandboxLevel = "minimal"
	// SandboxLevelStrict is the strongest policy. It blocks host sharing,
	// proxying, Rosetta, and vsock.
	SandboxLevelStrict SandboxLevel = "strict"
)

// ParseSandboxLevel parses a sandbox level string.
func ParseSandboxLevel(s string) (SandboxLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none", "off", "disabled", "default":
		return SandboxLevelUnset, nil
	case string(SandboxLevelMinimal):
		return SandboxLevelMinimal, nil
	case string(SandboxLevelStrict):
		return SandboxLevelStrict, nil
	default:
		return SandboxLevelUnset, fmt.Errorf("unknown sandbox level %q (use minimal or strict)", s)
	}
}

// SandboxPolicy describes the effective run policy for a VM.
type SandboxPolicy struct {
	Level SandboxLevel
}

// NewSandboxPolicy parses and validates a sandbox policy from text.
func NewSandboxPolicy(level string) (SandboxPolicy, error) {
	parsed, err := ParseSandboxLevel(level)
	if err != nil {
		return SandboxPolicy{}, err
	}
	return SandboxPolicy{Level: parsed}, nil
}

func currentSandboxPolicy() (SandboxPolicy, error) {
	return NewSandboxPolicy(sandboxLevel)
}

// Active reports whether any sandbox policy is in effect.
func (p SandboxPolicy) Active() bool {
	return p.Level == SandboxLevelMinimal || p.Level == SandboxLevelStrict
}

// Strict reports whether the strongest sandbox policy is in effect.
func (p SandboxPolicy) Strict() bool {
	return p.Level == SandboxLevelStrict
}

// AllowsVolumes reports whether host volume mounts are permitted.
func (p SandboxPolicy) AllowsVolumes() bool {
	return !p.Active()
}

// AllowsSharedFolders reports whether persisted GUI shared folders are permitted.
func (p SandboxPolicy) AllowsSharedFolders() bool {
	return !p.Active()
}

// AllowsVsock reports whether the guest should keep a vsock device.
func (p SandboxPolicy) AllowsVsock() bool {
	return p.Level != SandboxLevelStrict
}

// AllowsRosetta reports whether Rosetta should be enabled for Linux guests.
func (p SandboxPolicy) AllowsRosetta() bool {
	return p.Level != SandboxLevelStrict
}

// AllowsProxy reports whether guest proxy configuration is allowed.
func (p SandboxPolicy) AllowsProxy() bool {
	return p.Level != SandboxLevelStrict
}

// AllowsAgentProvision reports whether the host may auto-provision the guest agent.
func (p SandboxPolicy) AllowsAgentProvision() bool {
	return !p.Active()
}

// AllowsAgentUpgrade reports whether the host may auto-upgrade the guest agent.
func (p SandboxPolicy) AllowsAgentUpgrade() bool {
	return !p.Active()
}

// EffectiveNetworkMode returns the effective network mode for the policy.
//
// If explicit is false, the requested mode is treated as the current default
// rather than a user override.
func (p SandboxPolicy) EffectiveNetworkMode(requested string, explicit bool) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))

	switch p.Level {
	case SandboxLevelUnset:
		if requested == "" {
			return "nat", nil
		}
		return requested, nil
	case SandboxLevelMinimal:
		if explicit {
			if requested == "" {
				return "none", nil
			}
			return requested, nil
		}
		return "none", nil
	case SandboxLevelStrict:
		if explicit && requested != "" && requested != "none" {
			return "", fmt.Errorf("-sandbox-level strict does not allow -network %q; use minimal for network access", requested)
		}
		return "none", nil
	default:
		return "", fmt.Errorf("unknown sandbox level %q", p.Level)
	}
}

// EffectiveVolumes returns the volume mounts that should be applied.
func (p SandboxPolicy) EffectiveVolumes(cli, saved []vmconfig.VolumeMount) []vmconfig.VolumeMount {
	if !p.AllowsVolumes() {
		return nil
	}
	if len(cli) > 0 {
		return cloneVolumeMounts(cli)
	}
	return cloneVolumeMounts(saved)
}

// EffectiveSharedFolders returns the GUI shared folders that should be applied.
func (p SandboxPolicy) EffectiveSharedFolders(saved []SharedFolderEntry) []SharedFolderEntry {
	if !p.AllowsSharedFolders() {
		return nil
	}
	return cloneSharedFolderEntries(saved)
}

// EffectiveVsock reports whether the vsock device should remain present.
func (p SandboxPolicy) EffectiveVsock(requested bool) bool {
	if !requested {
		return false
	}
	return p.AllowsVsock()
}

// EffectiveRosetta reports whether Rosetta should remain enabled.
func (p SandboxPolicy) EffectiveRosetta(requested bool) bool {
	if !requested {
		return false
	}
	return p.AllowsRosetta()
}

// EffectiveProxy reports whether guest proxy configuration should be allowed.
func (p SandboxPolicy) EffectiveProxy(requested bool) bool {
	if !requested {
		return false
	}
	return p.AllowsProxy()
}

// EffectiveAgentUpgrade reports whether auto-upgrade should remain enabled.
func (p SandboxPolicy) EffectiveAgentUpgrade(requested bool) bool {
	if !requested {
		return false
	}
	return p.AllowsAgentUpgrade()
}

func sandboxActive() bool {
	policy, err := currentSandboxPolicy()
	return err == nil && policy.Active()
}

func sandboxStrict() bool {
	policy, err := currentSandboxPolicy()
	return err == nil && policy.Strict()
}

func sandboxAllowsVsock() bool {
	policy, err := currentSandboxPolicy()
	if err != nil {
		return true
	}
	return policy.AllowsVsock()
}

func sandboxAllowsAgentProvision() bool {
	policy, err := currentSandboxPolicy()
	if err != nil {
		return true
	}
	return policy.AllowsAgentProvision()
}

func sandboxAllowsAgentUpgrade() bool {
	policy, err := currentSandboxPolicy()
	if err != nil {
		return autoUpgradeAgent
	}
	return policy.EffectiveAgentUpgrade(autoUpgradeAgent)
}

func sandboxAllowsSharedFolders() bool {
	policy, err := currentSandboxPolicy()
	if err != nil {
		return true
	}
	return policy.AllowsSharedFolders()
}

func sandboxAllowsVolumes() bool {
	policy, err := currentSandboxPolicy()
	if err != nil {
		return true
	}
	return policy.AllowsVolumes()
}

func sandboxAllowsProxy() bool {
	policy, err := currentSandboxPolicy()
	if err != nil {
		return true
	}
	return policy.AllowsProxy()
}

func applySandboxDefaults() error {
	policy, err := currentSandboxPolicy()
	if err != nil {
		return err
	}
	proxySandboxLevel = sandboxLevel
	if !policy.Active() {
		return nil
	}

	if len(volumes) > 0 || strings.TrimSpace(shareDir) != "" {
		return fmt.Errorf("-sandbox-level %s does not allow -vol or -share-dir", policy.Level)
	}
	if !policy.AllowsProxy() && strings.TrimSpace(proxyURL) != "" {
		return fmt.Errorf("-sandbox-level strict does not allow -proxy; use minimal or omit -proxy")
	}
	if !policy.AllowsRosetta() && enableRosetta && flagWasSet("rosetta") {
		return fmt.Errorf("-sandbox-level strict does not allow -rosetta")
	}

	effectiveNetwork, err := policy.EffectiveNetworkMode(networkMode, flagWasSet("network"))
	if err != nil {
		return err
	}
	networkMode = effectiveNetwork
	enableClipboard = false
	autoMountVolumes = false
	autoUpgradeAgent = policy.EffectiveAgentUpgrade(autoUpgradeAgent)
	if !policy.AllowsRosetta() {
		enableRosetta = false
	}
	return nil
}

func effectiveSharedFolders(vmDirectory string) []SharedFolderEntry {
	policy, err := currentSandboxPolicy()
	if err != nil {
		return LoadSharedFolders(vmDirectory)
	}
	return policy.EffectiveSharedFolders(LoadSharedFolders(vmDirectory))
}

func sharedFolderCommandBlocked(args []string) bool {
	if !sandboxActive() || len(args) == 0 {
		return false
	}
	switch args[0] {
	case "list", "status", "mount", "help", "-h", "--help":
		return false
	default:
		return true
	}
}

func flagWasSet(name string) bool {
	seen := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			seen = true
		}
	})
	return seen
}

func cloneVolumeMounts(in []vmconfig.VolumeMount) []vmconfig.VolumeMount {
	if len(in) == 0 {
		return nil
	}
	out := make([]vmconfig.VolumeMount, len(in))
	copy(out, in)
	return out
}

func cloneSharedFolderEntries(in []SharedFolderEntry) []SharedFolderEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]SharedFolderEntry, len(in))
	copy(out, in)
	return out
}
