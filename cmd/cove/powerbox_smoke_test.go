//go:build darwin

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppSandboxPowerboxPromptSmoke(t *testing.T) {
	if os.Getenv("COVE_TEST_POWERBOX_UI") != "1" {
		t.Skip("set COVE_TEST_POWERBOX_UI=1 to run the interactive Powerbox prompt smoke")
	}
	bin, env := buildMacgoBundleSmokeBinary(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	containerHome := filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data")
	storeRoot := filepath.Join(containerHome, "tmp", "cove-powerbox-smoke")
	t.Cleanup(func() { _ = os.RemoveAll(storeRoot) })
	if err := os.MkdirAll(storeRoot, 0700); err != nil {
		t.Fatalf("create powerbox smoke root: %v", err)
	}
	store := filepath.Join(storeRoot, "bookmarks.json")
	grantEnv := append(append([]string{}, env...), securityBookmarkStoreEnv+"="+store)

	out, err := runSandboxSmokeCommandEnv(t, 5*time.Minute, grantEnv, bin, "security", "powerbox-prompt",
		"-json",
		"-store", store,
		"-key", "vm:powerbox-smoke",
		"-kind", "vm-root",
		"-title", "Grant cove test access",
		"-message", "Choose a test VM directory, then click Open.",
	)
	t.Logf("sandboxed macgo powerbox prompt err=%v output:\n%s", err, out)
	if err != nil {
		t.Fatalf("sandboxed macgo powerbox prompt: %v\n%s", err, out)
	}
	var report securityBookmarkStoreReport
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &report); err != nil {
		t.Fatalf("security powerbox-prompt json: %v\n%s", err, out)
	}
	if report.Key != "vm:powerbox-smoke" || report.Entry.BookmarkSize == 0 || report.Entry.Path == "" {
		t.Fatalf("powerbox prompt report incomplete: %+v\n%s", report, out)
	}
}
