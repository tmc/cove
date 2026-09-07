package firmware

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMountBridgeIntegration(t *testing.T) {
	if os.Getenv("COVE_TEST_BRIDGE") != "1" {
		t.Skip("set COVE_TEST_BRIDGE=1 for Swift/Cove mount integration")
	}
	cache := os.Getenv("COVE_TEST_PATCHER_DIR")
	developer := os.Getenv("COVE_TEST_DEVELOPER_DIR")
	helper := os.Getenv("COVE_TEST_COVE_BINARY")
	if cache == "" || developer == "" || helper == "" {
		t.Fatal("patcher cache, developer directory and signed Cove binary are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := BuildPatcher(ctx, cache, developer, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if report.OverlaySHA256 == "" {
		t.Fatal("missing overlay provenance")
	}
	directory, err := os.MkdirTemp("", "cove-bridge-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("bridge test artifacts:", directory)
	journal := filepath.Join(directory, "journal")
	image := filepath.Join(directory, "test.dmg")
	if _, err := diskImageCommand(ctx, "create", "-size", "64m", "-fs", "APFS", "-volname", "CoveBridgeTest", image); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(cache, "source")
	testSource := filepath.Join(source, "tests/FirmwarePatcherTests/FirmwarePatcherTests.swift")
	original, err := os.ReadFile(testSource)
	if err != nil {
		t.Fatal(err)
	}
	patchedTest := filepath.Join(directory, "FirmwarePatcherTests.swift")
	if err := os.WriteFile(patchedTest, append(original, []byte(bridgeSwiftTest)...), 0600); err != nil {
		t.Fatal(err)
	}
	baseOverlay := filepath.Join(cache, "overlays", report.OverlaySHA256, "overlay.json")
	data, err := os.ReadFile(baseOverlay)
	if err != nil {
		t.Fatal(err)
	}
	var overlay map[string]any
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatal(err)
	}
	overlay["roots"] = append(overlay["roots"].([]any), map[string]any{"type": "file", "name": testSource, "external-contents": patchedTest})
	overlayPath := filepath.Join(directory, "overlay.json")
	if err := writeState(overlayPath, overlay); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "swift", "test", "--filter", "CoveMountBridgeIntegrationTests", "-Xswiftc", "-vfsoverlay", "-Xswiftc", overlayPath)
	cmd.Dir = source
	cmd.Env = append(os.Environ(), "DEVELOPER_DIR="+developer, "COVE_MOUNT_HELPER="+helper, "COVE_MOUNT_JOURNAL="+journal, "COVE_TEST_MOUNT_IMAGE="+image)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := runPatcherCommand(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	j, err := OpenMountJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if len(j.state.Records) != 1 || j.state.Records[0].State != "detached" {
		t.Fatal("Swift bridge did not complete journaled cleanup", j.state)
	}
	live, err := j.find(ctx, j.state.Records[0])
	if err != nil || live != nil {
		t.Fatalf("bridge mount remains: %v", err)
	}
	j.Close()
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
}

const bridgeSwiftTest = `
import XCTest

final class CoveMountBridgeIntegrationTests: XCTestCase {
    func testJournaledMount() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let image = environment["COVE_TEST_MOUNT_IMAGE"],
              let journal = environment["COVE_MOUNT_JOURNAL"] else {
            throw XCTSkip("Cove bridge fixture is not configured")
        }
        let patcher = CryptexFilesystemPatcher(
            buildManiest: Data(), restoreDir: URL(fileURLWithPath: journal), verbose: false
        )
        let largeOutput = try patcher.runProcess("/usr/bin/head", ["-c", "131072", "/dev/zero"])
        XCTAssertEqual(largeOutput.utf8.count, 131072)
        let (device, mountPoint) = try patcher.attachImage(path: URL(fileURLWithPath: image), readonly: true)
        var attached = true
        defer { if attached { try? patcher.detachImage(deviceNode: device) } }
        let stateURL = URL(fileURLWithPath: journal).appendingPathComponent("mounts.json")
        let state = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: stateURL)) as? [String: Any])
        let records = try XCTUnwrap(state["records"] as? [[String: Any]])
        XCTAssertEqual(records.count, 1)
        XCTAssertEqual(records.first?["state"] as? String, "attached")
        XCTAssertFalse(device.isEmpty)
        XCTAssertFalse(mountPoint.isEmpty)
        try patcher.unmount(mount: mountPoint)
        try patcher.detachImage(deviceNode: device)
        attached = false
        let finalState = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: stateURL)) as? [String: Any])
        let finalRecords = try XCTUnwrap(finalState["records"] as? [[String: Any]])
        XCTAssertEqual(finalRecords.first?["state"] as? String, "detached")
    }
}
`
