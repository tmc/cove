package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	vmAgentPlatformMacOS = "macos"
	vmAgentPlatformLinux = "linux"

	vmAgentSourceInstall   = "install"
	vmAgentSourceProvision = "provision"
	vmAgentSourceUpgrade   = "upgrade"
	vmAgentSourceVerify    = "verify"
	vmAgentSourceRuntime   = "runtime"
)

// VMAgentConfig records durable guest-agent capability state in config.json.
type VMAgentConfig struct {
	Platform   string    `json:"platform,omitempty"`
	Requested  bool      `json:"requested,omitempty"`
	Verified   bool      `json:"verified,omitempty"`
	VerifiedAt time.Time `json:"verifiedAt,omitempty"`
	Source     string    `json:"source,omitempty"`
}

func cloneVMAgentConfig(cfg *VMAgentConfig) *VMAgentConfig {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	return &clone
}

func detectVMAgentPlatform(vmDirectory string) string {
	if _, err := os.Stat(filepath.Join(vmDirectory, "linux-disk.img")); err == nil {
		return vmAgentPlatformLinux
	}
	return vmAgentPlatformMacOS
}

func vmAgentPlatform(vmDirectory string) string {
	cfg, err := LoadVMConfig(vmDirectory)
	if err == nil && cfg.Agent != nil && cfg.Agent.Platform != "" {
		return cfg.Agent.Platform
	}
	return detectVMAgentPlatform(vmDirectory)
}

func updateVMAgentConfig(vmDirectory string, update func(*VMAgentConfig)) error {
	cfg, err := LoadVMConfig(vmDirectory)
	if err != nil {
		return fmt.Errorf("load vm config: %w", err)
	}

	agent := cloneVMAgentConfig(cfg.Agent)
	if agent == nil {
		agent = &VMAgentConfig{}
	}
	update(agent)
	if agent.Platform == "" {
		agent.Platform = detectVMAgentPlatform(vmDirectory)
	}
	cfg.Agent = agent

	if !agent.Requested && !agent.Verified && agent.VerifiedAt.IsZero() && agent.Source == "" {
		cfg.Agent = nil
	}
	if err := SaveVMConfig(vmDirectory, cfg); err != nil {
		return fmt.Errorf("save vm config: %w", err)
	}
	return nil
}

func setVMAgentRequested(vmDirectory, platform string, requested bool, source string) error {
	return updateVMAgentConfig(vmDirectory, func(agent *VMAgentConfig) {
		if platform != "" {
			agent.Platform = platform
		}
		agent.Requested = requested
		if source != "" {
			agent.Source = source
		}
	})
}

func markVMAgentVerified(vmDirectory, platform, source string, when time.Time) error {
	if when.IsZero() {
		when = time.Now()
	}
	return updateVMAgentConfig(vmDirectory, func(agent *VMAgentConfig) {
		if platform != "" {
			agent.Platform = platform
		}
		agent.Verified = true
		agent.VerifiedAt = when.UTC()
		if source != "" {
			agent.Source = source
		}
	})
}

func markCurrentVMAgentVerified(source string) error {
	if strings.TrimSpace(vmDir) == "" {
		return nil
	}
	return markVMAgentVerified(vmDir, detectVMAgentPlatform(vmDir), source, time.Now())
}

func markVMAgentVerifiedForSocket(sock, source string) error {
	if strings.TrimSpace(sock) == "" {
		return nil
	}
	vmDirectory := filepath.Dir(sock)
	return markVMAgentVerified(vmDirectory, detectVMAgentPlatform(vmDirectory), source, time.Now())
}
