package main

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/applicationservices"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/objc"

	"github.com/tmc/cove/internal/vmrun"
)

func TestReadClipboardText(t *testing.T) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_LAZY)
	if err != nil {
		t.Fatal(err)
	}
	defer purego.Dlclose(lib)
	// The generated binding treats this C function pointer as an ObjC block.
	var setPromiseKeeper func(applicationservices.PasteboardRef, uintptr, unsafe.Pointer) int32
	purego.RegisterLibFunc(&setPromiseKeeper, lib, "PasteboardSetPromiseKeeper")
	parent := t
	for _, tt := range []struct {
		name     string
		value    string
		promised bool
	}{
		{"text", "host text", false},
		{"empty", "", false},
		{"unicode", "世界 🌎", false},
		{"promised", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			objc.AutoreleasePool(func() {
				var ref applicationservices.PasteboardRef
				if status := applicationservices.PasteboardCreate(0, &ref); status != 0 {
					t.Fatalf("create pasteboard: %d", status)
				}
				parent.Cleanup(func() {
					applicationservices.PasteboardClear(ref)
					corefoundation.CFRelease(unsafe.Pointer(ref))
				})
				if status := applicationservices.PasteboardClear(ref); status != 0 {
					t.Fatalf("clear pasteboard: %d", status)
				}
				calls := 0
				keeper := purego.NewCallback(func(applicationservices.PasteboardRef, applicationservices.PasteboardItemID, corefoundation.CFStringRef, unsafe.Pointer) int32 {
					calls++
					return -1
				})
				if status := setPromiseKeeper(ref, keeper, nil); status != 0 {
					t.Fatalf("set promise keeper: %d", status)
				}
				var data corefoundation.CFDataRef
				if !tt.promised {
					data = corefoundation.CFDataCreate(0, []byte(tt.value), corefoundation.CFIndex(len(tt.value)))
					defer corefoundation.CFRelease(unsafe.Pointer(data))
				}
				item := applicationservices.PasteboardItemID(unsafe.Pointer(new(byte)))
				typeName := corefoundation.CFStringRef(objc.String("public.utf8-plain-text"))
				if status := applicationservices.PasteboardPutItemFlavor(ref, item, typeName, data, 0); status != 0 {
					t.Fatalf("put clipboard data: %d", status)
				}
				got := readClipboardText(ref, 42)
				if got.change != 42 || got.text != tt.value || got.available == tt.promised || got.pending != tt.promised {
					t.Fatalf("read clipboard = %+v", got)
				}
				if calls != 0 {
					t.Fatalf("resolved promised data %d times", calls)
				}
				if tt.promised {
					// Prove this fixture invokes the provider when read without the guard.
					applicationservices.PasteboardCopyItemFlavorData(ref, item, typeName, &data)
					if calls == 0 {
						t.Fatal("promise fixture did not invoke its provider")
					}
					value := []byte("materialized")
					data = corefoundation.CFDataCreate(0, value, corefoundation.CFIndex(len(value)))
					defer corefoundation.CFRelease(unsafe.Pointer(data))
					if status := applicationservices.PasteboardPutItemFlavor(ref, item, typeName, data, 0); status != 0 {
						t.Fatalf("materialize promise: %d", status)
					}
					got = readClipboardText(ref, 42)
					if got.text != string(value) || !got.available || got.pending {
						t.Fatalf("materialized clipboard = %+v", got)
					}
				}
			})
		})
	}
}

func TestClipboardPushPending(t *testing.T) {
	var state clipboardPushState
	text := clipboardText{change: 42, pending: true}
	if err := state.sync(text, func(string) error { t.Fatal("pushed promised text"); return nil }); err != nil {
		t.Fatal(err)
	}
	if state.synced {
		t.Fatal("marked promised text as synced")
	}
	text.pending, text.available, text.text = false, true, "materialized"
	calls := 0
	if err := state.sync(text, func(string) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("materialized text pushed %d times", calls)
	}
}

func TestHostClipboardReadCanceled(t *testing.T) {
	old := dispatchAsyncMainFn
	defer func() { dispatchAsyncMainFn = old }()
	var queued func()
	dispatchAsyncMainFn = func(fn func()) { queued = fn }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var reader hostClipboardReader
	if _, err := reader.read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("read error = %v", err)
	}
	queued()
}

func TestClipboardPushState(t *testing.T) {
	for _, tt := range []struct {
		name string
		text clipboardText
		want int
	}{
		{"text", clipboardText{change: 1, text: "host text", available: true}, 1},
		{"empty text", clipboardText{change: 1, available: true}, 1},
		{"non-text", clipboardText{change: 1}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var state clipboardPushState
			calls := 0
			push := func(value string) error {
				calls++
				if value != tt.text.text {
					t.Fatalf("pushed %q", value)
				}
				return nil
			}
			for i := 0; i < 2; i++ {
				if err := state.sync(tt.text, push); err != nil {
					t.Fatal(err)
				}
			}
			if calls != tt.want {
				t.Fatalf("calls = %d, want %d", calls, tt.want)
			}
		})
	}
}

func TestClipboardPushRetriesLatestAfterFailure(t *testing.T) {
	var state clipboardPushState
	old := clipboardText{change: 1, text: "old", available: true}
	if err := state.sync(old, func(string) error { return errors.New("user agent disconnected") }); err == nil {
		t.Fatal("missing failure")
	}
	latest := clipboardText{change: 2, text: "latest", available: true}
	var pushed string
	if err := state.sync(latest, func(value string) error { pushed = value; return nil }); err != nil {
		t.Fatal(err)
	}
	if pushed != "latest" {
		t.Fatalf("pushed %q", pushed)
	}
	if err := state.sync(latest, func(string) error { t.Fatal("repeated completed push"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestClipboardPushRetriesSameChange(t *testing.T) {
	var state clipboardPushState
	text := clipboardText{change: 1, text: "retry me", available: true}
	calls := 0
	push := func(string) error {
		calls++
		if calls == 1 {
			return errors.New("disconnected")
		}
		return nil
	}
	if err := state.sync(text, push); err == nil {
		t.Fatal("missing first failure")
	}
	if err := state.sync(text, push); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestClipboardPushSkipsOversizedText(t *testing.T) {
	var state clipboardPushState
	text := clipboardText{change: 1, text: string(make([]byte, clipboardTextLimit+1)), available: true}
	if err := state.sync(text, func(string) error { t.Fatal("pushed oversized text"); return nil }); err != nil {
		t.Fatal(err)
	}
	text.change++
	text.text = "small"
	called := false
	if err := state.sync(text, func(string) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("oversized clipboard suppressed later small copy")
	}
}

func TestClipboardMonitorDisabled(t *testing.T) {
	for _, tt := range []struct {
		name    string
		enabled bool
		profile string
	}{
		{"explicit opt-out", false, "full"},
		{"minimal runtime", true, "minimal"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := &ControlServer{runConfig: vmrun.RunConfig{EnableClipboard: tt.enabled}, hostConfig: vmrun.HostConfig{RuntimeProfile: tt.profile}}
			server.startLifecycleContext()
			defer server.shutdownLifecycleContext()
			done := make(chan struct{})
			go func() { defer close(done); server.monitorHostClipboard() }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("clipboard monitor started despite disabled sharing")
			}
		})
	}
}
