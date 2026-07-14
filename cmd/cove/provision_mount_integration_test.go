package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFinding3_TwoAttachedImages_Integration is the privileged, real-hardware
// counterpart to the deterministic unit tests in provision_mount_test.go. It
// attaches TWO real APFS disk images at once and asserts that the physical-store
// anchored discovery selects each image's OWN container and Data volume, never
// the other's — the concurrent-image hazard from the VM-lifecycle report
// (finding 3).
//
// It is capability-gated: it skips (does not fail) when the host lacks the
// tools or privilege to create/attach two APFS images. Cleanup detaches both
// images on every exit path.
func TestFinding3_TwoAttachedImages_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping privileged two-image integration test in -short mode")
	}
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("hdiutil not available")
	}
	if _, err := exec.LookPath("diskutil"); err != nil {
		t.Skip("diskutil not available")
	}
	// Attaching disk images does not strictly require root on macOS, but the
	// broader offline-provisioning matrix (ownership remount) does; gate the
	// privileged matrix explicitly and report the blocker rather than failing.
	if os.Getenv("COVE_PRIVILEGED_DISK_TESTS") == "" {
		t.Skip("set COVE_PRIVILEGED_DISK_TESTS=1 to run the privileged two-attached-image matrix")
	}

	dir := t.TempDir()

	// Create + attach two independent APFS images, each with a volume named
	// "Data" (so diskutil renders "APFS Volume Data", matching real VM disks).
	devA := createAndAttachAPFSData(t, filepath.Join(dir, "imgA"))
	devB := createAndAttachAPFSData(t, filepath.Join(dir, "imgB"))
	if devA == devB {
		t.Fatalf("both images attached to the same device %s", devA)
	}

	out, err := exec.Command("diskutil", "list").Output()
	if err != nil {
		t.Fatalf("diskutil list: %v", err)
	}
	listing := string(out)

	// For each device, discovery must land on that device's own container.
	contA := findAPFSContainerForDevice(listing, devA)
	contB := findAPFSContainerForDevice(listing, devB)
	if contA == "" || contB == "" {
		t.Fatalf("could not find containers: devA=%s->%q devB=%s->%q\n%s", devA, contA, devB, contB, listing)
	}
	if contA == contB {
		t.Fatalf("two distinct images resolved to the same container %q (devA=%s devB=%s)", contA, devA, devB)
	}

	// And each container's Data volume must belong to that container.
	dataA := dataVolumeForContainer(t, contA)
	dataB := dataVolumeForContainer(t, contB)
	if dataA == "" || dataB == "" {
		t.Fatalf("missing Data volume: contA=%s->%q contB=%s->%q", contA, dataA, contB, dataB)
	}
	if dataA == dataB {
		t.Fatalf("cross-selection: both containers yielded the same Data volume %q", dataA)
	}
	if !strings.HasPrefix(dataA, "/dev/"+contA+"s") {
		t.Errorf("Data volume %s is not in container %s", dataA, contA)
	}
	if !strings.HasPrefix(dataB, "/dev/"+contB+"s") {
		t.Errorf("Data volume %s is not in container %s", dataB, contB)
	}
	t.Logf("devA=%s container=%s data=%s", devA, contA, dataA)
	t.Logf("devB=%s container=%s data=%s", devB, contB, dataB)
}

// createAndAttachAPFSData creates an APFS sparse image with a "Data" volume,
// attaches it without mounting, registers cleanup to detach it, and returns the
// base /dev/diskN device.
func createAndAttachAPFSData(t *testing.T, imgBase string) string {
	t.Helper()
	cmd := exec.Command("hdiutil", "create",
		"-size", "50m",
		"-type", "SPARSE",
		"-fs", "APFS",
		"-volname", "Data",
		imgBase,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("hdiutil create failed (host lacks capability): %v\n%s", err, out)
	}
	imgPath := imgBase + ".sparseimage"

	out, err := exec.Command("hdiutil", "attach", imgPath, "-nobrowse", "-nomount").Output()
	if err != nil {
		t.Skipf("hdiutil attach failed (host lacks capability): %v", err)
	}
	device := parseBaseDevice(out)
	if device == "" {
		t.Fatalf("could not parse device from attach output:\n%s", out)
	}
	t.Cleanup(func() {
		// Detach by image path (robust) and by device (fallback), ignoring
		// errors so cleanup never masks the test result.
		_ = exec.Command("hdiutil", "detach", device, "-force").Run()
	})
	return device
}

func dataVolumeForContainer(t *testing.T, containerDisk string) string {
	t.Helper()
	out, err := exec.Command("diskutil", "list", "/dev/"+containerDisk).Output()
	if err != nil {
		t.Fatalf("diskutil list /dev/%s: %v", containerDisk, err)
	}
	return findDataVolumeInContainer(string(out), containerDisk)
}

var integrationBaseDiskRe = regexp.MustCompile(`/dev/(disk\d+)`)

// parseBaseDevice returns the first base /dev/diskN device in hdiutil attach
// output.
func parseBaseDevice(out []byte) string {
	m := integrationBaseDiskRe.FindSubmatch(out)
	if m == nil {
		return ""
	}
	return "/dev/" + string(m[1])
}
