// Package vmconfig loads and saves cove VM configuration files.
package vmconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	virtiofsx "github.com/tmc/apple/x/vzkit/virtiofs"
)

// VolumeMount represents a host-to-guest volume mount configuration.
type VolumeMount = virtiofsx.Mount

// AgentConfig records durable guest-agent capability state in config.json.
type AgentConfig struct {
	Platform   string    `json:"platform,omitempty"`
	Requested  bool      `json:"requested,omitempty"`
	Verified   bool      `json:"verified,omitempty"`
	VerifiedAt time.Time `json:"verifiedAt,omitempty"`
	Source     string    `json:"source,omitempty"`
}

// Config holds persistent configuration for a VM.
type Config struct {
	CPU                uint          `json:"cpu,omitempty"`
	MemoryGB           uint64        `json:"memoryGB,omitempty"`
	Volumes            []VolumeMount `json:"volumes,omitempty"`
	PostInstallRecipes string        `json:"postInstallRecipes,omitempty"`
	Agent              *AgentConfig  `json:"agent,omitempty"`
}

// Load reads dir/config.json. It returns an empty config if the file is missing.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read vm config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse vm config: %w", err)
	}
	return &cfg, nil
}

// Save writes cfg to dir/config.json.
func Save(dir string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vm config: %w", err)
	}
	path := filepath.Join(dir, "config.json")
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write vm config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename vm config: %w", err)
	}
	return nil
}

// SetPostInstallRecipes persists the selected post-install recipes.
func SetPostInstallRecipes(dir, recipes string) error {
	cfg, err := Load(dir)
	if err != nil {
		cfg = &Config{}
	}
	cfg.PostInstallRecipes = recipes
	return Save(dir, cfg)
}

// SetVolumes persists volume mounts.
func SetVolumes(dir string, mounts []VolumeMount) error {
	cfg, err := Load(dir)
	if err != nil {
		return err
	}
	cfg.Volumes = mounts
	return Save(dir, cfg)
}
