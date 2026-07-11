package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentLegacyArtifactPaths(t *testing.T) {
	set := make(map[string]bool)
	for _, p := range agentLegacyArtifactPaths() {
		set[p] = true
	}

	wantLegacy := []string{
		filepath.Join("Library", "LaunchDaemons", "com.vz.agent.plist"),
		filepath.Join("Library", "LaunchDaemons", "com.github.tmc.vz-macos.vz-agent.plist"),
		filepath.Join("Library", "LaunchAgents", "com.github.tmc.vz-macos.vz-agent-user.plist"),
	}
	for _, p := range wantLegacy {
		if !set[p] {
			t.Errorf("legacy artifact %s not listed for removal", p)
		}
	}

	current := []string{
		filepath.Join("Library", "LaunchDaemons", agentLaunchDaemonLabel+".plist"),
		filepath.Join("Library", "LaunchAgents", agentLaunchAgentLabel+".plist"),
		filepath.Join("usr", "local", "bin", agentBinaryName),
	}
	for _, p := range current {
		if set[p] {
			t.Errorf("current install path %s must not be listed as legacy", p)
		}
	}
}

// TestAgentInjectManifestSupersedesLegacy checks the offline-injection
// manifest installs the current agent and removes legacy artifacts in the
// same privileged pass, and never removes a path it installs.
func TestAgentInjectManifestSupersedesLegacy(t *testing.T) {
	mount := filepath.Join(string(filepath.Separator), "Volumes", "Data")
	em := agentInjectManifest(mount, "disk9s5", "/tmp/vz-agent", "/tmp/daemon.plist", "/tmp/agent.plist")

	installed := make(map[string]bool)
	for _, c := range em.CopyFiles {
		installed[c.Dst] = true
	}
	for _, want := range []string{
		filepath.Join(mount, "usr", "local", "bin", agentBinaryName),
		filepath.Join(mount, "Library", "LaunchDaemons", agentLaunchDaemonLabel+".plist"),
		filepath.Join(mount, "Library", "LaunchAgents", agentLaunchAgentLabel+".plist"),
	} {
		if !installed[want] {
			t.Errorf("manifest does not install %s", want)
		}
	}

	removed := make(map[string]bool)
	for _, p := range em.RemoveFiles {
		removed[p] = true
	}
	for _, p := range agentLegacyRemovals(mount) {
		if !removed[p] {
			t.Errorf("manifest does not remove legacy artifact %s", p)
		}
	}
	for _, c := range em.CopyFiles {
		if removed[c.Dst] {
			t.Errorf("manifest removes a path it installs: %s", c.Dst)
		}
	}
}

// TestAgentLegacyMigrationIdempotent simulates a mounted Data volume holding
// both legacy and current agent files and applies the same removal step the
// elevated manifest runs. Legacy files must disappear, current files must
// survive, and a second pass must succeed with nothing left to remove.
func TestAgentLegacyMigrationIdempotent(t *testing.T) {
	mount := t.TempDir()

	legacy := []string{
		filepath.Join(mount, "Library", "LaunchDaemons", "com.vz.agent.plist"),
		filepath.Join(mount, "Library", "LaunchDaemons", "com.github.tmc.vz-macos.vz-agent.plist"),
		filepath.Join(mount, "Library", "LaunchAgents", "com.github.tmc.vz-macos.vz-agent-user.plist"),
	}
	current := []string{
		filepath.Join(mount, "Library", "LaunchDaemons", agentLaunchDaemonLabel+".plist"),
		filepath.Join(mount, "Library", "LaunchAgents", agentLaunchAgentLabel+".plist"),
		filepath.Join(mount, "usr", "local", "bin", agentBinaryName),
	}
	for _, p := range append(append([]string{}, legacy...), current...) {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := len(agentLegacyRemovalsPresent(mount)), len(legacy); got != want {
		t.Fatalf("agentLegacyRemovalsPresent = %d paths, want %d", got, want)
	}

	if err := removeFilesIgnoreMissing(agentLegacyRemovals(mount)); err != nil {
		t.Fatalf("first removal pass: %v", err)
	}
	for _, p := range legacy {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("legacy file survived migration: %s", p)
		}
	}
	for _, p := range current {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("current file removed by migration: %s (%v)", p, err)
		}
	}

	// Second pass: nothing legacy left, must still succeed.
	if err := removeFilesIgnoreMissing(agentLegacyRemovals(mount)); err != nil {
		t.Fatalf("second removal pass: %v", err)
	}
	if present := agentLegacyRemovalsPresent(mount); len(present) != 0 {
		t.Errorf("legacy artifacts still present after migration: %v", present)
	}
}

func TestAgentLegacyRemoveScript(t *testing.T) {
	script := agentLegacyRemoveScript()
	for _, label := range agentLegacyLaunchdLabels {
		if !strings.Contains(script, label+".plist") {
			t.Errorf("remove script does not mention %s", label)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(script), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, `rm -f "$MOUNT/`) {
			t.Errorf("unexpected script line %q", line)
		}
	}
	if strings.Contains(script, agentLaunchDaemonLabel+".plist\"") || strings.Contains(script, agentLaunchAgentLabel+".plist\"") {
		t.Errorf("remove script deletes current plists:\n%s", script)
	}
}
