//go:build darwin || linux

package firmware

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/apple/x/plist"
)

var fixtureComponents = []string{"LLB", "iBSS", "iBEC", "iBoot", "DeviceTree", "RestoreDeviceTree", "SEP", "RestoreSEP", "KernelCache", "RestoreKernelCache", "RestoreRamDisk", "OS", "SystemVolume", "StaticTrustCache", "Ap,SystemVolumeCanonicalMetadata", "Ap,RestoreSecurePageTableMonitor", "Ap,RestoreTrustedExecutionMonitor", "Ap,SecurePageTableMonitor", "Ap,TrustedExecutionMonitor", "RecoveryMode", "RestoreTrustCache"}

func fixturePlist(t *testing.T, v any) []byte {
	t.Helper()
	b, err := plist.Marshal(v, plist.FormatXML)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func fixtureZip(t *testing.T, name string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, data := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
func fixtureIdentity(board, variant, path string) map[string]any {
	components := make(map[string]any)
	for _, key := range fixtureComponents {
		components[key] = map[string]any{"Info": map[string]any{"Path": path}}
	}
	return map[string]any{"ApBoardID": "0x90", "ApChipID": "0xFE01", "Info": map[string]any{"DeviceClass": board, "Variant": variant}, "Manifest": components}
}
func fixtureTree(t *testing.T, root string) {
	t.Helper()
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"payload":                    []byte("firmware"),
		"BuildManifest.plist":        fixturePlist(t, map[string]any{"BuildIdentities": []any{fixtureIdentity("vresearch101ap", "Erase", "payload")}}),
		"Restore.plist":              fixturePlist(t, map[string]any{"ProductVersion": "test"}),
		"iPhone-BuildManifest.plist": fixturePlist(t, map[string]any{"ProductVersion": "test"}),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPreparePublishAndReuse(t *testing.T) {
	dir := t.TempDir()
	iphone, cloud := filepath.Join(dir, "iphone.ipsw"), filepath.Join(dir, "cloud.ipsw")
	fixtureZip(t, iphone, map[string][]byte{"one": []byte("iphone")})
	fixtureZip(t, cloud, map[string][]byte{"two": []byte("cloud")})
	output := filepath.Join(dir, "prepared")
	calls := 0
	run := func(ctx context.Context, work, cache, iphone, cloud string, log io.Writer) error {
		calls++
		fixtureTree(t, filepath.Join(work, "iphone_Restore"))
		return nil
	}
	for range 2 {
		if err := prepare(context.Background(), iphone, cloud, output, run, nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("ran recipe %d times", calls)
	}
	var state preparedState
	b, err := os.ReadFile(filepath.Join(output, "firmware.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	if state.Stage != "prepared" || state.Attempt == "" || len(state.Outputs) != 4 {
		t.Fatal(state)
	}
	if err := os.WriteFile(filepath.Join(output, state.Tree, "payload"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepare(context.Background(), iphone, cloud, output, run, nil); err == nil {
		t.Fatal("reused changed tree")
	}
	if calls != 1 {
		t.Fatal("mutated published tree")
	}
}

func TestPrepareFailedAttempt(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "input.ipsw")
	fixtureZip(t, archive, map[string][]byte{"file": []byte("data")})
	output := filepath.Join(dir, "prepared")
	fail := func(context.Context, string, string, string, string, io.Writer) error {
		return errors.New("interrupted")
	}
	if err := prepare(context.Background(), archive, archive, output, fail, nil); err == nil {
		t.Fatal("missing failure")
	}
	if _, err := os.Stat(filepath.Join(output, "firmware.json")); !os.IsNotExist(err) {
		t.Fatal("published failed attempt", err)
	}
	run := func(ctx context.Context, work, cache, iphone, cloud string, log io.Writer) error {
		fixtureTree(t, filepath.Join(work, "iphone_Restore"))
		return nil
	}
	if err := prepare(context.Background(), archive, archive, output, run, nil); err != nil {
		t.Fatal(err)
	}
	attempts, _ := filepath.Glob(filepath.Join(output, "attempt-*"))
	if len(attempts) != 2 {
		t.Fatal(attempts)
	}
}

func TestArchivePaths(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a/../b", "a\\b"} {
		t.Run(name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "input.zip")
			fixtureZip(t, archive, map[string][]byte{name: []byte("x")})
			if _, err := archiveDigest(context.Background(), archive); err == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
}
func TestPrepareMissingComponent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	fixtureTree(t, root)
	if err := os.Remove(filepath.Join(root, "payload")); err != nil {
		t.Fatal(err)
	}
	if err := validateTree(root); err == nil {
		t.Fatal("accepted missing component")
	}
}

// Set COVE_TEST_VPHONE_SOURCE to the pinned checkout to exercise the real recipe
// with synthetic archives. No network, VM, mount or privileged operation is used.
func TestPreparePinnedRecipe(t *testing.T) {
	source := os.Getenv("COVE_TEST_VPHONE_SOURCE")
	if source == "" {
		t.Skip("set COVE_TEST_VPHONE_SOURCE for pinned recipe integration")
	}
	dir := t.TempDir()
	iphone, cloud := filepath.Join(dir, "iphone.ipsw"), filepath.Join(dir, "cloud.ipsw")
	restore := map[string]any{"ProductBuildVersion": "test", "ProductVersion": "1", "DeviceMap": []any{map[string]any{"BoardConfig": "vresearch101ap"}, map[string]any{"BoardConfig": "vphone600ap"}}, "SupportedProductTypeIDs": map[string]any{"DFU": []any{1}, "Recovery": []any{2}}, "SupportedProductTypes": []any{"test"}, "SystemRestoreImageFileSystems": map[string]any{"OS": "apfs"}}
	makeFiles := func(identities []any, payload string) map[string][]byte {
		files := map[string][]byte{"BuildManifest.plist": fixturePlist(t, map[string]any{"BuildIdentities": identities, "ManifestVersion": 1, "ProductBuildVersion": "test", "ProductVersion": "1"}), "Restore.plist": fixturePlist(t, restore), payload: []byte("payload"), "kernelcache.test": []byte("kernel"), "Firmware/shared.im4p": []byte("firmware")}
		for _, sub := range []string{"agx", "all_flash", "ane", "dfu", "pmp"} {
			files["Firmware/"+sub+"/test.im4p"] = []byte("component")
		}
		return files
	}
	fixtureZip(t, iphone, makeFiles([]any{fixtureIdentity("iphone", "Erase", "iphone.dmg")}, "iphone.dmg"))
	fixtureZip(t, cloud, makeFiles([]any{fixtureIdentity("vresearch101ap", "Erase", "cloud.dmg"), fixtureIdentity("vresearch101ap", "Research", "cloud.dmg"), fixtureIdentity("vphone600ap", "Erase", "cloud.dmg"), fixtureIdentity("vphone600ap", "Research", "cloud.dmg")}, "cloud.dmg"))
	if err := Prepare(context.Background(), source, iphone, cloud, filepath.Join(dir, "prepared"), os.Stderr); err != nil {
		t.Fatal(err)
	}
}
func ExamplePrepare() {
	fmt.Println(Prepare(context.Background(), "", "", "", "", nil))
	// Output: source, iphone, cloudos and output paths are required
}

func TestPrepareCancellation(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "input.ipsw")
	fixtureZip(t, archive, map[string][]byte{"file": []byte("data")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := func(ctx context.Context, work, cache, iphone, cloud string, log io.Writer) error {
		fixtureTree(t, filepath.Join(work, "iphone_Restore"))
		cancel()
		return nil
	}
	output := filepath.Join(dir, "prepared")
	if err := prepare(ctx, archive, archive, output, run, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "firmware.json")); !os.IsNotExist(err) {
		t.Fatal("published canceled attempt", err)
	}
}
