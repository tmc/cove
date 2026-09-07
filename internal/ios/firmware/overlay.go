//go:build darwin || linux

package firmware

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed bridge/mount.swift
var mountBridgeSwift string

func patchMountSource(original string) (string, error) {
	start := "        let process = Process()\n        process.executableURL = URL(fileURLWithPath: \"/usr/bin/hdiutil\")"
	end := "        let root = try parsePlist(data: data)"
	if strings.Count(original, start) != 1 {
		return "", fmt.Errorf("pinned attach implementation differs")
	}
	begin := strings.Index(original, start)
	tail := strings.Index(original[begin:], end)
	if tail < 0 {
		return "", fmt.Errorf("pinned attach parser is missing")
	}
	patched := original[:begin] + "        let data = try CoveMountBridge.run([\"attach\"] + (readonly ? [\"-readonly\"] : []) + [path.path])\n\n" + original[begin+tail:]
	for _, r := range []struct{ old, new string }{
		{`"-c", "diskutil image resize --plist \"\(output.path)\" | plutil -extract max raw -o - -"`, `"-c", "/usr/sbin/diskutil image resize --plist \"$1\" | /usr/bin/plutil -extract max raw -o - -", "cove-resize", output.path`},
		{`try runProcess("/usr/bin/hdiutil", ["detach", deviceNode])`, `try CoveMountBridge.run(["detach", deviceNode])`},
		{`try runProcess("/usr/sbin/diskutil", ["unmount", mount])`, `try CoveMountBridge.run(["unmount", mount])`},
		{`try runProcess("/sbin/mount", ["-u", "-w", device, mountPoint])`, `try CoveMountBridge.run(["remount", device, mountPoint])`},
	} {
		if strings.Count(patched, r.old) != 1 {
			return "", fmt.Errorf("pinned mount operation differs: %s", r.old)
		}
		patched = strings.Replace(patched, r.old, r.new, 1)
	}
	readOutput := "        let output = output == nil ? String(data: outPipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) : nil"
	oldWait := "        process.waitUntilExit()\n\n" + readOutput
	if strings.Count(patched, oldWait) != 1 {
		return "", fmt.Errorf("pinned subprocess output handling differs")
	}
	patched = strings.Replace(patched, oldWait, readOutput+"\n        process.waitUntilExit()", 1)
	return patched + "\n" + mountBridgeSwift, nil
}

func preparePatcherOverlay(ctx context.Context, directory, source string) (string, string, error) {
	target := filepath.Join(source, "sources/FirmwarePatcher/Filesystem/CryptexFilesystemPatcher.swift")
	data, err := os.ReadFile(target)
	if err != nil {
		return "", "", err
	}
	patched, err := patchMountSource(string(data))
	if err != nil {
		return "", "", err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(patched)))
	dir := filepath.Join(directory, "overlays", digest)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", err
	}
	contents := filepath.Join(dir, "CryptexFilesystemPatcher.swift")
	if err := writeBuildInfo(contents, []byte(patched)); err != nil {
		return "", "", err
	}
	overlay := filepath.Join(dir, "overlay.json")
	descriptor := map[string]any{"version": 0, "case-sensitive": false, "roots": []any{map[string]any{"type": "file", "name": target, "external-contents": contents}}}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := writeState(overlay, descriptor); err != nil {
		return "", "", err
	}
	if err := syncTree(dir); err != nil {
		return "", "", err
	}
	return overlay, digest, nil
}
