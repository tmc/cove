// agent_migrate.go - Removal of obsolete guest agent launchd artifacts.
//
// The guest agent's launchd labels have been renamed twice:
//
//	com.vz.agent                          -> com.github.tmc.vz-macos.vz-agent -> com.tmc.cove.vz-agent
//	com.github.tmc.vz-macos.vz-agent-user -> com.tmc.cove.vz-agent-user
//
// A VM provisioned before a rename keeps the old plist on disk. After
// re-provisioning, launchd loads both generations and the two KeepAlive'd
// agents fight over vsock ports 1024/1025, so both channels loop
// disconnected/deadline_exceeded. Offline injection therefore removes every
// known obsolete plist in the same elevated operation that installs the
// current ones — legacy artifacts are only dropped when superseded.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// agentLegacyLaunchdLabels lists launchd labels previously used by the guest
// agent daemon and user agent. A plist with any of these names is obsolete
// once the current labels are installed.
var agentLegacyLaunchdLabels = []string{
	"com.vz.agent",
	"com.github.tmc.vz-macos.vz-agent",
	"com.github.tmc.vz-macos.vz-agent-user",
}

// agentLegacyBinaryPaths lists guest paths (relative to the volume root) of
// agent binaries the current install no longer writes. The binary has lived
// at /usr/local/bin/vz-agent across every label rename — the current install
// overwrites it in place — so the list is empty today; add entries here if
// the binary path ever moves.
var agentLegacyBinaryPaths []string

// agentLegacyArtifactPaths returns the guest paths, relative to the volume
// root, of all known obsolete agent plists and binaries. Each legacy label is
// swept from both LaunchDaemons and LaunchAgents so a misplaced plist is
// removed too.
func agentLegacyArtifactPaths() []string {
	var paths []string
	for _, label := range agentLegacyLaunchdLabels {
		paths = append(paths,
			filepath.Join("Library", "LaunchDaemons", label+".plist"),
			filepath.Join("Library", "LaunchAgents", label+".plist"),
		)
	}
	return append(paths, agentLegacyBinaryPaths...)
}

// agentLegacyRemovals resolves agentLegacyArtifactPaths against a mounted
// Data volume, for use as elevatedManifest.RemoveFiles.
func agentLegacyRemovals(mountPoint string) []string {
	rel := agentLegacyArtifactPaths()
	abs := make([]string, len(rel))
	for i, p := range rel {
		abs[i] = filepath.Join(mountPoint, p)
	}
	return abs
}

// agentLegacyRemovalsPresent returns the subset of agentLegacyRemovals that
// exist on disk, for reporting what a migration will actually remove.
func agentLegacyRemovalsPresent(mountPoint string) []string {
	var present []string
	for _, p := range agentLegacyRemovals(mountPoint) {
		if _, err := os.Stat(p); err == nil {
			present = append(present, p)
		}
	}
	return present
}

// agentLegacyRemoveScript returns shell lines that delete every known
// obsolete agent artifact under $MOUNT. Used by the restricted-environment
// installer script, which performs the install without the elevation
// manifest. rm -f keeps re-runs idempotent.
func agentLegacyRemoveScript() string {
	var b strings.Builder
	b.WriteString("# Remove obsolete agent plists from earlier label generations.\n")
	for _, p := range agentLegacyArtifactPaths() {
		fmt.Fprintf(&b, "rm -f \"$MOUNT/%s\"\n", p)
	}
	return b.String()
}
