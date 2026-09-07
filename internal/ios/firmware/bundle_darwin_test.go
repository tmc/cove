package firmware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func bundleFixture(t *testing.T) (PatchOptions, patchedState) {
	t.Helper()
	o := patchFixture(t, "regular")
	if err := patch(context.Background(), o, Patcher{SourceCommit: SourceCommit}, fixturePatch(t, "regular"), func(string) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(o.Output, "patched.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state patchedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return o, state
}

func TestBundleLease(t *testing.T) {
	o, _ := bundleFixture(t)
	// Published outputs remain usable after their original inputs are removed.
	if err := os.RemoveAll(o.Prepared); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(o.ROM); err != nil {
		t.Fatal(err)
	}
	b, err := OpenPatched(context.Background(), o.Output)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	f, err := b.Open(context.Background(), "iphone_Restore/payload")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err := errors.Join(err, f.Close()); err != nil {
		t.Fatal(err)
	}
	if string(data) != "patched firmware" {
		t.Fatalf("payload = %q", data)
	}
	lock, err := os.OpenFile(filepath.Join(o.Output, ".patch.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("exclusive lease while bundle open: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(context.Background(), "iphone_Restore/payload"); err == nil {
		t.Fatal("opened closed bundle")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("exclusive lease after close: %v", err)
	}
	if _, err := OpenPatched(context.Background(), o.Output); err == nil {
		t.Fatal("opened exclusively leased bundle")
	}
}

func TestBundleRejectsChangedOutput(t *testing.T) {
	for _, name := range []string{"changed", "missing", "extra", "symlink", "fifo", "log", "records", "source", "tree", "catalog", "receipt fifo", "lock fifo"} {
		t.Run(name, func(t *testing.T) {
			o, state := bundleFixture(t)
			payload := filepath.Join(o.Output, state.Tree, "iphone_Restore", "payload")
			write := func(path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			remove := func(path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "changed":
				write(payload)
			case "missing":
				remove(payload)
			case "extra":
				write(payload + ".extra")
			case "symlink":
				remove(payload)
				if err := os.Symlink(o.ROM, payload); err != nil {
					t.Fatal(err)
				}
			case "fifo", "receipt fifo", "lock fifo":
				path := payload
				if name == "receipt fifo" {
					path = filepath.Join(o.Output, "patched.json")
				} else if name == "lock fifo" {
					path = filepath.Join(o.Output, ".patch.lock")
				}
				remove(path)
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "log":
				write(filepath.Join(o.Output, state.Attempt, "patch.log"))
			case "records":
				write(filepath.Join(o.Output, state.Attempt, "records.json"))
			case "source", "tree", "catalog":
				switch name {
				case "source":
					state.Patcher.SourceCommit = "other"
				case "tree":
					state.Tree = "../work"
				case "catalog":
					state.Outputs["../escape"] = strings.Repeat("0", 64)
				}
				if err := writeState(filepath.Join(o.Output, "patched.json"), state); err != nil {
					t.Fatal(err)
				}
			}
			b, err := OpenPatched(context.Background(), o.Output)
			if err == nil {
				b.Close()
				t.Fatal("accepted changed output")
			}
		})
	}
}

func TestBundleOpenChecks(t *testing.T) {
	o, state := bundleFixture(t)
	b, err := OpenPatched(context.Background(), o.Output)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, name := range []string{"../payload", "/payload", "iphone_Restore/../AVPBooter.bin", "iphone_Restore\\payload", "unrecorded"} {
		if f, err := b.Open(context.Background(), name); err == nil {
			f.Close()
			t.Fatalf("opened %q", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Open(ctx, "iphone_Restore/payload"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open: %v", err)
	}
	if err := os.WriteFile(filepath.Join(o.Output, state.Tree, "iphone_Restore", "payload"), []byte("changed after verification"), 0600); err != nil {
		t.Fatal(err)
	}
	if f, err := b.Open(context.Background(), "iphone_Restore/payload"); err == nil {
		f.Close()
		t.Fatal("opened changed payload")
	}
}

func ExampleOpenPatched() {
	_, err := OpenPatched(context.Background(), "")
	fmt.Println(err)
	// Output: patched bundle directory is required
}

func ExampleBundle_Open() {
	var b Bundle
	_, err := b.Open(context.Background(), "iphone_Restore/BuildManifest.plist")
	fmt.Println(err)
	// Output: patched bundle is closed
}

func ExampleBundle_Close() {
	var b Bundle
	fmt.Println(b.Close())
	// Output: <nil>
}
