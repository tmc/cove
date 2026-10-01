package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishCopy(t *testing.T) {
	for _, directory := range []bool{false, true} {
		for _, overwrite := range []bool{false, true} {
			t.Run(fmtCopyTestName(directory, overwrite), func(t *testing.T) {
				root := t.TempDir()
				stage := filepath.Join(root, "stage")
				dest := filepath.Join(root, "dest")
				if directory {
					os.Mkdir(stage, 0700)
					os.WriteFile(filepath.Join(stage, "new"), []byte("new"), 0600)
				} else {
					os.WriteFile(stage, []byte("new"), 0600)
				}
				// Simulate a destination created after transfer began, including a broken symlink.
				if err := os.Symlink(filepath.Join(root, "missing"), dest); err != nil {
					t.Fatal(err)
				}
				err := publishCopy(stage, dest, overwrite)
				if !overwrite {
					if err == nil {
						t.Fatal("replaced concurrent destination")
					}
					if _, err := os.Readlink(dest); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Readlink(stage); directory && err != nil {
					t.Fatalf("old destination was not retained for cleanup: %v", err)
				}
				if directory {
					dest = filepath.Join(dest, "new")
				}
				data, err := os.ReadFile(dest)
				if err != nil || string(data) != "new" {
					t.Fatalf("published %q: %v", data, err)
				}
			})
		}
	}
}

func fmtCopyTestName(dir, overwrite bool) string {
	name := "file"
	if dir {
		name = "directory"
	}
	if overwrite {
		name += " overwrite"
	}
	return name
}

func TestCopyFileToHostFailure(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "destination")
	os.WriteFile(dest, []byte("old"), 0600)
	err := copyFileToHost(context.Background(), dest, true, func(stage string) error {
		os.WriteFile(stage, []byte("partial"), 0600)
		return errors.New("transfer interrupted")
	})
	if err == nil {
		t.Fatal("copy succeeded")
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "old" {
		t.Fatalf("destination changed: %q", data)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("staging files remain: %v", entries)
	}
}

func TestWindowsCopyPublishScript(t *testing.T) {
	script := windowsCopyPublishScript(`C:\a'b`, `C:\dest`, false)
	if strings.Contains(script, "Replace") || !strings.Contains(script, "[IO.File]::Move($src,$dst)") || !strings.Contains(script, "a''b") {
		t.Fatalf("unsafe no-overwrite script: %s", script)
	}
	if !strings.Contains(windowsCopyPublishScript("a", "b", true), "[IO.File]::Replace") {
		t.Fatal("overwrite does not replace")
	}
}

func TestCopyDirectoryToHostInterrupted(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "old"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "source/new", Mode: 0600, Size: 10}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("partial"))
	err := copyDirectoryToHost(context.Background(), dest, true, func(w io.Writer) error { _, err := w.Write(archive.Bytes()); return err })
	if err == nil {
		t.Fatal("truncated archive succeeded")
	}
	data, err := os.ReadFile(filepath.Join(dest, "old"))
	if err != nil || string(data) != "old" {
		t.Fatalf("existing destination changed: %q %v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("partial directory remains: %v %v", entries, err)
	}
}

func TestCopyFileToHostCanceledBeforePublication(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "destination")
	ctx, cancel := context.WithCancel(context.Background())
	err := copyFileToHost(ctx, dest, false, func(stage string) error {
		if err := os.WriteFile(stage, []byte("complete"), 0600); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("published canceled copy: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging output remains: %v %v", entries, err)
	}
}
