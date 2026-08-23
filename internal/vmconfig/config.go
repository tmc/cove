// Package vmconfig loads and saves cove VM configuration files.
package vmconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	virtiofsx "github.com/tmc/apple/x/vzkit/virtiofs"
	"github.com/tmc/cove/internal/iosbundle"
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
	Version    string    `json:"version,omitempty"`
	Commit     string    `json:"commit,omitempty"`
	Features   []string  `json:"features,omitempty"`
}

// Config holds persistent configuration for a VM.
type Config struct {
	IOS                *iosbundle.Config `json:"ios,omitempty"`
	CPU                uint              `json:"cpu,omitempty"`
	MemoryGB           uint64            `json:"memoryGB,omitempty"`
	GuestUserUID       uint32            `json:"guestUserUID,omitempty"`
	GuestUserGID       uint32            `json:"guestUserGID,omitempty"`
	Volumes            []VolumeMount     `json:"volumes,omitempty"`
	PostInstallRecipes string            `json:"postInstallRecipes,omitempty"`
	Agent              *AgentConfig      `json:"agent,omitempty"`
	ParentVM           string            `json:"parentVM,omitempty"`
	ParentSnapshot     string            `json:"parentSnapshot,omitempty"`
	ParentImage        string            `json:"parentImage,omitempty"`
	ForkedAt           time.Time         `json:"forkedAt,omitempty"`
}

// Hardware holds CPU and memory settings for a VM.
type Hardware struct {
	CPU      uint
	MemoryGB uint64
}

// HardwareExplicit records which hardware fields were explicitly requested.
type HardwareExplicit struct {
	CPU      bool
	MemoryGB bool
}

// Load reads dir/config.json. It returns an empty config if the file is missing.
func Load(dir string) (*Config, error) {
	return load(dir)
}

// load reads dir/config.json without taking the config lock.
func load(dir string) (*Config, error) {
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
	if cfg.IOS != nil {
		if err := cfg.IOS.Validate(); err != nil {
			return nil, fmt.Errorf("parse vm config: %w", err)
		}
	}
	return &cfg, nil
}

// Save writes cfg to dir/config.json.
func Save(dir string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("save vm config: nil config")
	}
	if cfg.IOS != nil {
		if err := cfg.IOS.Validate(); err != nil {
			return fmt.Errorf("save vm config: %w", err)
		}
	}
	return withConfigLock(dir, func() error { return save(dir, cfg) })
}

// save writes cfg to dir/config.json without taking the config lock.
//
// The temp file name is unique per writer, so concurrent writers cannot
// interleave on a shared scratch path and rename a partial file into
// place: each rename publishes one writer's complete config.
func save(dir string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vm config: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(dir, "config.json")
	tmp, err := os.CreateTemp(dir, "config-*.json")
	if err != nil {
		return fmt.Errorf("create vm config temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write vm config: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync vm config: %w", err)
	}
	if err = tmp.Chmod(0644); err != nil {
		return fmt.Errorf("chmod vm config: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close vm config: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename vm config: %w", err)
	}
	return nil
}

// Update runs mutate against the VM's saved config and persists the
// result, holding the config lock across the whole read-modify-write so
// concurrent updaters cannot drop each other's changes. mutate reports
// whether anything changed; an unchanged config is not rewritten.
//
// mutate must not call back into Load, Save, or any SetXxx function:
// the config lock is not reentrant.
func Update(dir string, mutate func(cfg *Config) (bool, error)) (bool, error) {
	changed := false
	err := withConfigLock(dir, func() error {
		cfg, err := load(dir)
		if err != nil {
			return err
		}
		changed, err = mutate(cfg)
		if err != nil || !changed {
			return err
		}
		return save(dir, cfg)
	})
	return changed, err
}

// ApplyHardware resolves runtime hardware settings against cfg.
//
// Saved values are used when the matching field was not explicitly requested.
// Explicit runtime values update cfg and report changed=true when persistence
// is needed.
func ApplyHardware(cfg *Config, current Hardware, explicit HardwareExplicit) (Hardware, bool) {
	changed := false
	next := current
	if !explicit.CPU && cfg.CPU > 0 {
		next.CPU = cfg.CPU
	} else if explicit.CPU && cfg.CPU != current.CPU {
		cfg.CPU = current.CPU
		changed = true
	}

	if !explicit.MemoryGB && cfg.MemoryGB > 0 {
		next.MemoryGB = cfg.MemoryGB
	} else if explicit.MemoryGB && cfg.MemoryGB != current.MemoryGB {
		cfg.MemoryGB = current.MemoryGB
		changed = true
	}
	return next, changed
}

// SetHardware persists CPU and memory settings.
func SetHardware(dir string, hardware Hardware) (bool, error) {
	return Update(dir, func(cfg *Config) (bool, error) {
		if cfg.CPU == hardware.CPU && cfg.MemoryGB == hardware.MemoryGB {
			return false, nil
		}
		cfg.CPU = hardware.CPU
		cfg.MemoryGB = hardware.MemoryGB
		return true, nil
	})
}

func SetGuestUser(dir string, uid, gid uint32) error {
	_, err := Update(dir, func(cfg *Config) (bool, error) {
		cfg.GuestUserUID = uid
		cfg.GuestUserGID = gid
		return true, nil
	})
	return err
}

// SetPostInstallRecipes persists the selected post-install recipes.
// An unreadable config is replaced rather than reported.
func SetPostInstallRecipes(dir, recipes string) error {
	return withConfigLock(dir, func() error {
		cfg, err := load(dir)
		if err != nil {
			cfg = &Config{}
		}
		cfg.PostInstallRecipes = recipes
		return save(dir, cfg)
	})
}

// SetVolumes persists volume mounts.
func SetVolumes(dir string, mounts []VolumeMount) error {
	_, err := Update(dir, func(cfg *Config) (bool, error) {
		cfg.Volumes = mounts
		return true, nil
	})
	return err
}
