package firmware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func patchFixture(t *testing.T, variant string) PatchOptions {
	t.Helper()
	root := t.TempDir()
	prepared := filepath.Join(root, "prepared")
	if err := os.Mkdir(prepared, 0700); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(prepared, "iphone_Restore")
	fixtureTree(t, tree)
	hashes, err := treeHashes(context.Background(), tree)
	if err != nil {
		t.Fatal(err)
	}
	state := preparedState{SchemaVersion: 1, Stage: "prepared", SourceCommit: SourceCommit, Tree: "iphone_Restore", Outputs: hashes}
	if err := writeState(filepath.Join(prepared, "firmware.json"), state); err != nil {
		t.Fatal(err)
	}
	rom := filepath.Join(root, "rom.bin")
	if err := os.WriteFile(rom, []byte("original rom"), 0600); err != nil {
		t.Fatal(err)
	}
	return PatchOptions{Prepared: prepared, ROM: rom, Output: filepath.Join(root, "patched"), Variant: variant, Helper: rom}
}

func fixturePatchRecords(variant string) []patchRecord {
	zero := int64(0)
	var records []patchRecord
	for _, component := range []string{"ibec", "llb", "devicetree"} {
		records = append(records, patchRecord{PatchID: component + ".test", Component: component, FileOffset: &zero, OriginalBytes: []byte{0}, PatchedBytes: []byte{1}})
	}
	if variant == "less" {
		for _, id := range []string{"filesystem.cryptex.merge", "manifest.hash"} {
			records = append(records, patchRecord{PatchID: id, FileOffset: &zero, OriginalBytes: []byte{}, PatchedBytes: []byte{}})
		}
	} else {
		for _, component := range []string{"avpbooter", "ibss", "txm", "kernelcache"} {
			records = append(records, patchRecord{PatchID: component + ".test", Component: component, FileOffset: &zero, OriginalBytes: []byte{0}, PatchedBytes: []byte{1}})
		}
	}
	for _, p := range []struct {
		enabled bool
		id      string
	}{{variant == "dev" || variant == "jb" || variant == "exp", "txm_dev.get_task_allow"}, {variant == "jb" || variant == "exp", "ibss_jb.skip_generate_nonce"}, {variant == "jb" || variant == "exp", "kernelcache_jb.thid_should_crash"}, {variant == "exp", "kernelcache_exp.hv_vmm_oid_rename"}} {
		if p.enabled {
			records = append(records, patchRecord{PatchID: p.id, Component: "kernelcache", FileOffset: &zero, OriginalBytes: []byte{0}, PatchedBytes: []byte{1}})
		}
	}
	return records
}

func fixturePatch(t *testing.T, variant string) func(context.Context, string, string, *os.File, io.Writer) error {
	t.Helper()
	return func(ctx context.Context, attempt, work string, lease *os.File, log io.Writer) error {
		if err := os.WriteFile(filepath.Join(work, "iphone_Restore", "payload"), []byte("patched firmware"), 0600); err != nil {
			return err
		}
		if _, err := io.WriteString(log, "patched fixture\n"); err != nil {
			return err
		}
		return writeState(filepath.Join(attempt, "records.json"), fixturePatchRecords(variant))
	}
}

func TestPatchVariants(t *testing.T) {
	for _, variant := range []string{"less", "regular", "dev", "jb", "exp"} {
		t.Run(variant, func(t *testing.T) {
			o := patchFixture(t, variant)
			run := fixturePatch(t, variant)
			calls := 0
			callback := func(ctx context.Context, a, w string, lease *os.File, log io.Writer) error {
				calls++
				return run(ctx, a, w, lease, log)
			}
			cleanup := func(string) error { return nil }
			for range 2 {
				if err := patch(context.Background(), o, Patcher{}, callback, cleanup, nil); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 1 {
				t.Fatalf("ran patcher %d times", calls)
			}
			data, err := os.ReadFile(filepath.Join(o.Prepared, "iphone_Restore", "payload"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "firmware" {
				t.Fatal("mutated prepared input")
			}
			var state patchedState
			data, err = os.ReadFile(filepath.Join(o.Output, "patched.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if state.Stage != "patched" || state.RecordsSHA256 == "" {
				t.Fatal(state)
			}
			if err := os.WriteFile(filepath.Join(o.Output, state.Tree, "AVPBooter.bin"), []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := patch(context.Background(), o, Patcher{}, callback, cleanup, nil); err == nil {
				t.Fatal("reused modified output")
			}
		})
	}
}

func TestPatchFailureRecovery(t *testing.T) {
	for _, mode := range []string{"child", "cleanup", "canceled", "records", "diagnostic", "unchanged"} {
		t.Run(mode, func(t *testing.T) {
			o := patchFixture(t, "regular")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			exited := false
			recovered := false
			run := func(ctx context.Context, a, w string, lease *os.File, log io.Writer) error {
				defer func() { exited = true }()
				if mode == "child" {
					return errors.New("child failed")
				}
				if mode == "canceled" {
					cancel()
				}
				if mode == "diagnostic" {
					fmt.Fprintln(log, "  [-] patch site absent")
				}
				if mode == "unchanged" {
					return writeState(filepath.Join(a, "records.json"), fixturePatchRecords("regular"))
				}
				if err := fixturePatch(t, "regular")(ctx, a, w, lease, log); err != nil {
					return err
				}
				if mode == "records" {
					return os.WriteFile(filepath.Join(a, "records.json"), []byte("[]"), 0600)
				}
				return nil
			}
			cleanup := func(string) error {
				if !exited {
					t.Fatal("cleanup before child exit")
				}
				recovered = true
				if mode == "cleanup" {
					return errors.New("attachment remains")
				}
				return nil
			}
			if err := patch(ctx, o, Patcher{}, run, cleanup, nil); err == nil {
				t.Fatal("published failed patch")
			}
			if !recovered {
				t.Fatal("did not recover")
			}
			if _, err := os.Stat(filepath.Join(o.Output, "patched.json")); !os.IsNotExist(err) {
				t.Fatal("published failed attempt", err)
			}
			attempts, _ := filepath.Glob(filepath.Join(o.Output, "attempt-*/attempt.json"))
			if len(attempts) != 1 {
				t.Fatal(attempts)
			}
			var state patchedState
			b, err := os.ReadFile(attempts[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &state); err != nil {
				t.Fatal(err)
			}
			if state.Stage != "failed" || state.Error == "" {
				t.Fatal(state)
			}
			recovered = false
			retryCleanup := func(string) error { recovered = true; return nil }
			retry := func(ctx context.Context, a, w string, lease *os.File, log io.Writer) error {
				if !recovered {
					t.Fatal("retry before recovery")
				}
				return fixturePatch(t, "regular")(ctx, a, w, lease, log)
			}
			if err := patch(context.Background(), o, Patcher{}, retry, retryCleanup, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPatchOptionValidation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options PatchOptions
		want    string
	}{
		{"unknown", PatchOptions{Variant: "other"}, "unknown firmware variant"},
		{"binpack", PatchOptions{Variant: "regular", NoBinpack: true}, "require less"},
		{"vphoned", PatchOptions{Variant: "jb", NoVphoned: true}, "require less"},
		{"frida", PatchOptions{Variant: "dev", Frida: true}, "frida requires"},
		{"guard", PatchOptions{Variant: "dev", ForceExcGuard: true}, "force-exc-guard requires"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.options.validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestPatchRecordsRejectInvalid(t *testing.T) {
	for _, data := range []string{"[]", `[{"patchID":"test","component":"ibec","patchedBytes":"AA=="}]`, `[{"patchID":"test","component":"ibec","fileOffset":-1}]`, "[] {}"} {
		name := filepath.Join(t.TempDir(), "records.json")
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validatePatchRecords(name, PatchOptions{Variant: "regular"}); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestPatchInheritedLease(t *testing.T) {
	name := filepath.Join(t.TempDir(), "lock")
	lease, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/cat")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.ExtraFiles = []*os.File{lease}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Wait() }()
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := os.OpenFile(name, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("child lost workflow lease")
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal("lease remained after child exit", err)
	}
}

func ExamplePatch() {
	fmt.Println(Patch(context.Background(), PatchOptions{Variant: "regular"}, nil))
	// Output: toolchain, prepared, rom, output and helper paths are required
}

func TestPatchPinnedFailure(t *testing.T) {
	toolchain := os.Getenv("COVE_TEST_PATCHER_DIR")
	helper := os.Getenv("COVE_TEST_COVE_BINARY")
	if toolchain == "" || helper == "" {
		t.Skip("set COVE_TEST_PATCHER_DIR and COVE_TEST_COVE_BINARY for pinned patcher integration")
	}
	o := patchFixture(t, "regular")
	o.Toolchain = toolchain
	o.Helper = helper
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := Patch(ctx, o, os.Stderr)
	if err == nil {
		t.Fatal("patched invalid fixture firmware")
	}
	attempts, _ := filepath.Glob(filepath.Join(o.Output, "attempt-*/patch.log"))
	if len(attempts) != 1 {
		t.Fatalf("patcher did not run: %v", err)
	}
	log, readErr := os.ReadFile(attempts[0])
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(log), "AVPBooter") {
		t.Fatalf("did not reach AVPBooter: %v\n%s", err, log)
	}
	if _, err := os.Stat(filepath.Join(o.Output, "patched.json")); !os.IsNotExist(err) {
		t.Fatal("published invalid firmware", err)
	}
}

func TestPatchLockExcludesRetry(t *testing.T) {
	o := patchFixture(t, "regular")
	run := func(ctx context.Context, a, w string, lease *os.File, log io.Writer) error {
		called := false
		retry := func(context.Context, string, string, *os.File, io.Writer) error { called = true; return nil }
		err := patch(ctx, o, Patcher{}, retry, func(string) error { t.Fatal("recovery without workflow lock"); return nil }, nil)
		if err == nil || !strings.Contains(err.Error(), "lock patch output") || called {
			t.Fatal("concurrent retry accepted", err)
		}
		return fixturePatch(t, "regular")(ctx, a, w, lease, log)
	}
	if err := patch(context.Background(), o, Patcher{}, run, func(string) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPatchRecordsRequireRequestedFeatures(t *testing.T) {
	for _, options := range []PatchOptions{{Variant: "jb", Frida: true}, {Variant: "regular", ForceExcGuard: true}} {
		name := filepath.Join(t.TempDir(), "records.json")
		records := fixturePatchRecords(options.Variant)
		if err := writeState(name, records); err != nil {
			t.Fatal(err)
		}
		if err := validatePatchRecords(name, options); err == nil {
			t.Fatal("accepted missing requested patch")
		}
		id := "kernel.thread_guard_violation"
		if options.Frida {
			id = "kernelcache_frida.thread_set_state_entitlement_flag"
		}
		zero := int64(0)
		records = append(records, patchRecord{PatchID: id, Component: "kernelcache", FileOffset: &zero, OriginalBytes: []byte{0}, PatchedBytes: []byte{1}})
		if err := writeState(name, records); err != nil {
			t.Fatal(err)
		}
		if err := validatePatchRecords(name, options); err != nil {
			t.Fatal(err)
		}
	}
}
