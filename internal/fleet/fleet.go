// Package fleet stores remote cove host registrations.
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Config struct {
	Remotes map[string]Remote `json:"remotes,omitempty"`
}

type Remote struct {
	Host      string   `json:"host"`
	User      string   `json:"user,omitempty"`
	SSHArgs   []string `json:"ssh_args,omitempty"`
	DefaultVM string   `json:"default_vm,omitempty"`
}

type Entry struct {
	Name string
	Remote
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "fleet.json")
}

func LoadPath(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Remotes: make(map[string]Remote)}, nil
		}
		return nil, fmt.Errorf("read fleet config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse fleet config: %w", err)
	}
	if cfg.Remotes == nil {
		cfg.Remotes = make(map[string]Remote)
	}
	return &cfg, nil
}

func SavePath(path string, cfg *Config) error {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Remotes == nil {
		cfg.Remotes = make(map[string]Remote)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create fleet config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal fleet config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write fleet config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename fleet config: %w", err)
	}
	return nil
}

func (c *Config) Add(name string, remote Remote) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("fleet name required")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("invalid fleet name %q", name)
	}
	if strings.TrimSpace(remote.Host) == "" {
		return fmt.Errorf("fleet host required")
	}
	if c.Remotes == nil {
		c.Remotes = make(map[string]Remote)
	}
	if _, ok := c.Remotes[name]; ok {
		return fmt.Errorf("fleet remote %q already exists", name)
	}
	remote.Host = strings.TrimSpace(remote.Host)
	remote.User = strings.TrimSpace(remote.User)
	remote.DefaultVM = strings.TrimSpace(remote.DefaultVM)
	c.Remotes[name] = remote
	return nil
}

func (c *Config) Remove(name string) error {
	if c == nil || c.Remotes == nil {
		return fmt.Errorf("fleet remote %q not found", name)
	}
	if _, ok := c.Remotes[name]; !ok {
		return fmt.Errorf("fleet remote %q not found", name)
	}
	delete(c.Remotes, name)
	return nil
}

func (c *Config) Get(name string) (Remote, bool) {
	if c == nil || c.Remotes == nil {
		return Remote{}, false
	}
	r, ok := c.Remotes[name]
	return r, ok
}

func (c *Config) List() []Entry {
	if c == nil || len(c.Remotes) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Remotes))
	for name := range c.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Entry, 0, len(names))
	for _, name := range names {
		out = append(out, Entry{Name: name, Remote: c.Remotes[name]})
	}
	return out
}

func ParseTarget(s string) (Remote, error) {
	target := strings.TrimSpace(s)
	if target == "" {
		return Remote{}, fmt.Errorf("fleet target required")
	}
	var r Remote
	if user, host, ok := strings.Cut(target, "@"); ok {
		r.User = strings.TrimSpace(user)
		r.Host = strings.TrimSpace(host)
	} else {
		r.Host = target
	}
	if r.Host == "" {
		return Remote{}, fmt.Errorf("fleet host required")
	}
	return r, nil
}
