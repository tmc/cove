package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

const (
	PlatformMacOS = "macos"
	PlatformLinux = "linux"

	SourceInstall   = "install"
	SourceProvision = "provision"
	SourceUpgrade   = "upgrade"
	SourceVerify    = "verify"
	SourceRuntime   = "runtime"
)

// CloneConfig returns a shallow copy of cfg.
func CloneConfig(cfg *vmconfig.AgentConfig) *vmconfig.AgentConfig {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	return &clone
}

// DetectPlatform infers the guest platform from the VM directory.
func DetectPlatform(vmDirectory string) string {
	if _, err := os.Stat(filepath.Join(vmDirectory, "linux-disk.img")); err == nil {
		return PlatformLinux
	}
	return PlatformMacOS
}

// Platform returns the persisted platform, falling back to DetectPlatform.
func Platform(vmDirectory string) string {
	cfg, err := vmconfig.Load(vmDirectory)
	if err == nil && cfg.Agent != nil && cfg.Agent.Platform != "" {
		return cfg.Agent.Platform
	}
	return DetectPlatform(vmDirectory)
}

func updateConfig(vmDirectory string, update func(*vmconfig.AgentConfig)) error {
	cfg, err := vmconfig.Load(vmDirectory)
	if err != nil {
		return fmt.Errorf("load vm config: %w", err)
	}

	agent := CloneConfig(cfg.Agent)
	if agent == nil {
		agent = &vmconfig.AgentConfig{}
	}
	update(agent)
	if agent.Platform == "" {
		agent.Platform = DetectPlatform(vmDirectory)
	}
	cfg.Agent = agent

	if !agent.Requested && !agent.Verified && agent.VerifiedAt.IsZero() && agent.Source == "" {
		cfg.Agent = nil
	}
	if err := vmconfig.Save(vmDirectory, cfg); err != nil {
		return fmt.Errorf("save vm config: %w", err)
	}
	return nil
}

// SetRequested records whether the VM should have the guest agent installed.
func SetRequested(vmDirectory, platform string, requested bool, source string) error {
	return updateConfig(vmDirectory, func(agent *vmconfig.AgentConfig) {
		if platform != "" {
			agent.Platform = platform
		}
		agent.Requested = requested
		if source != "" {
			agent.Source = source
		}
	})
}

// MarkVerified records a successful guest-agent connection.
func MarkVerified(vmDirectory, platform, source string, when time.Time) error {
	return MarkVerifiedInfo(vmDirectory, platform, source, "", "", nil, when)
}

// MarkVerifiedInfo records a successful guest-agent connection and the
// agent's advertised build identity / features.
func MarkVerifiedInfo(vmDirectory, platform, source, version, commit string, features []string, when time.Time) error {
	if when.IsZero() {
		when = time.Now()
	}
	return updateConfig(vmDirectory, func(agent *vmconfig.AgentConfig) {
		if platform != "" {
			agent.Platform = platform
		}
		agent.Verified = true
		agent.VerifiedAt = when.UTC()
		if source != "" {
			agent.Source = source
		}
		if version != "" {
			agent.Version = version
		}
		if commit != "" {
			agent.Commit = commit
		}
		if len(features) > 0 {
			agent.Features = append([]string(nil), features...)
		}
	})
}

// MarkVerifiedForSocket records verification for the VM that owns sock.
func MarkVerifiedForSocket(sock, source string) error {
	if strings.TrimSpace(sock) == "" {
		return nil
	}
	vmDirectory := filepath.Dir(sock)
	return MarkVerified(vmDirectory, DetectPlatform(vmDirectory), source, time.Now())
}
