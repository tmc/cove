// volume_validate.go — guard saved volume mounts before they become
// directory-sharing devices.
//
// A VZVirtioFileSystemDeviceConfiguration whose shared directory points at a
// missing host path, or two devices sharing one tag, makes macOS 26+/27
// reject the entire VM configuration at start with VZErrorDomain code=2
// ("A directory sharing device configuration is invalid"). One stale saved
// volume then takes down an otherwise valid VM. validateVolumes filters such
// mounts out before the device is built so the VM still starts, and
// hostDoctorVolumeSharesCheck flags them so they can be cleaned up.

package main

import (
	"fmt"
	"os"
	"strings"

	virtiofsx "github.com/tmc/apple/x/vzkit/virtiofs"
	"github.com/tmc/cove/internal/vmconfig"
)

// volumeIssue records a saved volume mount that cannot be turned into a valid
// directory-sharing device, with a human-readable reason.
type volumeIssue struct {
	Mount  vmconfig.VolumeMount
	Reason string
}

// hostPathStat is a seam for tests; production uses os.Stat.
var hostPathStat = os.Stat

// validateVolumes partitions mounts into those safe to attach (kept) and
// those that must be skipped (skipped) because attaching them would make
// macOS reject the whole VM configuration. It rejects mounts with an empty
// or missing host path, a host path that is not a directory, or a duplicate
// explicit tag. Empty tags are auto-assigned per host base name downstream by
// virtiofsx and are not deduplicated here.
func validateVolumes(mounts []vmconfig.VolumeMount) (kept []vmconfig.VolumeMount, skipped []volumeIssue) {
	seenTags := make(map[string]bool)
	for _, m := range mounts {
		if strings.TrimSpace(m.HostPath) == "" {
			skipped = append(skipped, volumeIssue{m, "empty host path"})
			continue
		}
		info, err := hostPathStat(m.HostPath)
		switch {
		case err != nil:
			skipped = append(skipped, volumeIssue{m, "host path missing: " + m.HostPath})
			continue
		case !info.IsDir():
			skipped = append(skipped, volumeIssue{m, "host path is not a directory: " + m.HostPath})
			continue
		}
		if m.Tag != "" && m.Tag != virtiofsx.MacOSAutomountTag {
			if seenTags[m.Tag] {
				skipped = append(skipped, volumeIssue{m, "duplicate tag: " + m.Tag})
				continue
			}
			seenTags[m.Tag] = true
		}
		kept = append(kept, m)
	}
	return kept, skipped
}

// volumeIssueLabel names a volume in a warning, preferring its tag and
// falling back to the host path.
func volumeIssueLabel(m vmconfig.VolumeMount) string {
	if m.Tag != "" && m.Tag != virtiofsx.MacOSAutomountTag {
		return "tag " + m.Tag
	}
	if m.HostPath != "" {
		return m.HostPath
	}
	return "(unnamed)"
}

// hostDoctorVolumeSharesCheck flags VMs whose saved volumes reference missing
// host paths or duplicate tags. Such volumes are skipped at start (so the VM
// still boots) but should be repaired in the VM's config.json.
func hostDoctorVolumeSharesCheck() hostDoctorCheck {
	vms, err := vmProcessListVMs()
	if err != nil {
		return hostDoctorCheck{"volume-shares", "warn", "could not list VMs: " + err.Error()}
	}
	var problems []string
	for _, vm := range vms {
		cfg, err := vmconfig.Load(vm.Path)
		if err != nil || cfg == nil || len(cfg.Volumes) == 0 {
			continue
		}
		if _, skipped := validateVolumes(cfg.Volumes); len(skipped) > 0 {
			for _, s := range skipped {
				problems = append(problems, fmt.Sprintf("%s: %s", vm.Name, s.Reason))
			}
		}
	}
	if len(problems) == 0 {
		return hostDoctorCheck{"volume-shares", "pass", "no VM saved volumes reference missing host paths or duplicate tags"}
	}
	msg := strings.Join(problems, "; ") +
		"; cove skips these at start, but repair or remove them in the VM's config.json"
	return hostDoctorCheck{"volume-shares", "warn", msg}
}
