package main

import (
	"path/filepath"
	"testing"
)

func TestAgentUpgradeStageRejectsInvalidHostPath(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", dir, filepath.Join(dir, "missing")} {
		t.Run(name, func(t *testing.T) {
			response := (&ControlServer{}).handleAgentUpgradeStage(name)
			if response.Success || response.Error != "upgrade binary must be a regular file" {
				t.Fatalf("response = %v", response)
			}
		})
	}
}
