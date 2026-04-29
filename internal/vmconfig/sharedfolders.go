package vmconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SharedFolderEntry represents a persisted shared folder configuration.
type SharedFolderEntry struct {
	Path     string `json:"path"`
	Tag      string `json:"tag"`
	ReadOnly bool   `json:"readOnly"`
}

// LoadSharedFolders loads persisted shared folder entries from the VM directory.
func LoadSharedFolders(vmDirectory string) []SharedFolderEntry {
	configPath := filepath.Join(vmDirectory, "shared_folders.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	var folders []SharedFolderEntry
	if err := json.Unmarshal(data, &folders); err != nil {
		return nil
	}
	return folders
}

func SaveSharedFolders(vmDirectory string, folders []SharedFolderEntry) error {
	configPath := filepath.Join(vmDirectory, "shared_folders.json")
	if len(folders) == 0 {
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove shared folder config: %w", err)
		}
		return nil
	}
	data, err := json.MarshalIndent(folders, "", "  ")
	if err != nil {
		return fmt.Errorf("encode shared folders: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("write shared folder config: %w", err)
	}
	return nil
}

func UniqueSharedFolderTag(base string, existing []SharedFolderEntry) string {
	base = SanitizeSharedFolderTag(base)
	taken := make(map[string]bool, len(existing))
	for _, f := range existing {
		taken[f.Tag] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func SanitizeSharedFolderTag(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return "share"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range base {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() == 0 || lastDash {
				continue
			}
			b.WriteByte('-')
			lastDash = true
		}
	}
	tag := strings.Trim(b.String(), "-")
	if tag == "" {
		return "share"
	}
	return tag
}
