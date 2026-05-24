package imagestore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

// Manifest is the on-disk schema for an image's manifest.json.
type Manifest struct {
	SchemaVersion  int              `json:"schemaVersion"`
	Name           string           `json:"name"`
	Tag            string           `json:"tag"`
	OSType         string           `json:"osType,omitempty"`
	SourceVM       string           `json:"sourceVM,omitempty"`
	BaseImage      string           `json:"baseImage,omitempty"`
	CoveCommit     string           `json:"cove_commit,omitempty"`
	AgentCommit    string           `json:"agent_commit,omitempty"`
	AgentFeatures  []string         `json:"agent_features,omitempty"`
	BuildRecipe    string           `json:"build_recipe,omitempty"`
	SourceImage    string           `json:"source_image,omitempty"`
	BuiltAt        time.Time        `json:"built_at,omitempty"`
	DefaultNetwork string           `json:"default_network,omitempty"`
	DefaultSandbox string           `json:"default_sandbox,omitempty"`
	DiskSHA256     string           `json:"diskSHA256"`
	DiskSize       int64            `json:"diskSize"`
	CreatedAt      time.Time        `json:"createdAt"`
	SourceConfig   *vmconfig.Config `json:"sourceConfig,omitempty"`
}

// Entry is a row in an image listing.
type Entry struct {
	Ref      Ref
	Manifest *Manifest
}

// LoadManifest reads the manifest at ref.
func LoadManifest(ref Ref) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(ref.Path(), "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read image manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse image manifest: %w", err)
	}
	return &m, nil
}

// Exists reports whether the image at ref has a manifest on disk.
func Exists(ref Ref) bool {
	_, err := os.Stat(filepath.Join(ref.Path(), "manifest.json"))
	return err == nil
}

// WriteManifest writes manifest.json in dir atomically.
func WriteManifest(dir string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	path := filepath.Join(dir, "manifest.json")
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync manifest: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename manifest: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
