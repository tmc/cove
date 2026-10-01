package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

type debugScreenshotTransport struct {
	setupAssistantTransport
	capture func() (image.Image, error)
}

func (t debugScreenshotTransport) Screenshot() (image.Image, error) { return t.capture() }

func screenshotTestAssistant(path string, capture func() (image.Image, error)) *SetupAssistant {
	return &SetupAssistant{saveDir: path, transport: debugScreenshotTransport{capture: capture}}
}

func TestSetupAssistantScreenshotCreatesOnlyFinalDirectory(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "screenshots")
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	s := screenshotTestAssistant(path, func() (image.Image, error) { return img, nil })
	s.saveDebugScreenshot("test")
	files, err := filepath.Glob(filepath.Join(path, "*.png"))
	if err != nil || len(files) != 1 {
		t.Fatalf("screenshots = %v, %v, want one", files, err)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := png.Decode(f); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(parent, "missing", "screenshots")
	called := false
	screenshotTestAssistant(missing, func() (image.Image, error) { called = true; return img, nil }).saveDebugScreenshot("test")
	if called {
		t.Fatal("captured after missing parent")
	}
	if _, err := os.Lstat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatalf("missing parent recreated: %v", err)
	}
}

func TestSetupAssistantScreenshotRejectsDirectorySymlink(t *testing.T) {
	parent, target := t.TempDir(), t.TempDir()
	path := filepath.Join(parent, "screenshots")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	called := false
	screenshotTestAssistant(path, func() (image.Image, error) { called = true; return nil, nil }).saveDebugScreenshot("test")
	if called {
		t.Fatal("captured through directory symlink")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink target changed: %v, %v", entries, err)
	}
}

func TestSetupAssistantScreenshotDoesNotRecreateVMRoot(t *testing.T) {
	t.Setenv(vmconfig.StateDirEnv, t.TempDir())
	if err := os.MkdirAll(vmconfig.BaseDir(), 0700); err != nil {
		t.Fatal(err)
	}
	path := vmconfig.Path("deleted")
	called := false
	screenshotTestAssistant(path, func() (image.Image, error) { called = true; return nil, nil }).saveDebugScreenshot("test")
	if called {
		t.Fatal("captured after missing vm root")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("vm root recreated: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "vms")
	if err := os.Symlink(vmconfig.BaseDir(), alias); err != nil {
		t.Fatal(err)
	}
	screenshotTestAssistant(filepath.Join(alias, "deleted.covevm"), func() (image.Image, error) { called = true; return nil, nil }).saveDebugScreenshot("test")
	if called {
		t.Fatal("captured after aliased missing vm root")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("vm root recreated through alias: %v", err)
	}
}

func TestSetupAssistantScreenshotRetainsOpenedDirectory(t *testing.T) {
	for _, operation := range []string{"remove", "rename"} {
		t.Run(operation, func(t *testing.T) {
			parent := t.TempDir()
			vm := filepath.Join(parent, "vm")
			path := filepath.Join(vm, "screenshots")
			if err := os.Mkdir(vm, 0700); err != nil {
				t.Fatal(err)
			}
			moved := filepath.Join(parent, "moved")
			s := screenshotTestAssistant(path, func() (image.Image, error) {
				var err error
				if operation == "remove" {
					err = os.RemoveAll(vm)
				} else {
					err = os.Rename(vm, moved)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(vm, 0700); err != nil {
					t.Fatal(err)
				}
				return image.NewRGBA(image.Rect(0, 0, 4, 4)), nil
			})
			s.saveDebugScreenshot("test")
			entries, err := os.ReadDir(vm)
			if err != nil || len(entries) != 0 {
				t.Fatalf("replacement vm changed: %v, %v", entries, err)
			}
			if operation == "rename" {
				files, err := filepath.Glob(filepath.Join(moved, "screenshots", "*.png"))
				if err != nil || len(files) != 1 {
					t.Fatalf("opened directory screenshot = %v, %v, want one", files, err)
				}
			}
		})
	}
}

func TestSetupAssistantScreenshotDoesNotFollowFilenameSymlink(t *testing.T) {
	path := t.TempDir()
	target := filepath.Join(t.TempDir(), "retained")
	if err := os.WriteFile(target, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	s := screenshotTestAssistant(path, func() (image.Image, error) {
		now := time.Now().Unix()
		for second := now; second <= now+2; second++ {
			if err := os.Symlink(target, filepath.Join(path, fmt.Sprintf("test_%d.png", second))); err != nil {
				t.Fatal(err)
			}
		}
		return image.NewRGBA(image.Rect(0, 0, 4, 4)), nil
	})
	s.saveDebugScreenshot("test")
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "retained" {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
}

func TestSetupAssistantScreenshotRejectsUnboundedImage(t *testing.T) {
	path := t.TempDir()
	screenshotTestAssistant(path, func() (image.Image, error) { return image.NewUniform(color.Black), nil }).saveDebugScreenshot("test")
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unbounded image written: %v, %v", entries, err)
	}
	var output bytes.Buffer
	w := debugScreenshotWriter{writer: &output, remaining: 4}
	if _, err := w.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("5")); err == nil || output.String() != "1234" {
		t.Fatalf("size bound failed: %q, %v", output.String(), err)
	}
}
