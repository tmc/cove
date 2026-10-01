package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestGuestCopyInstall(t *testing.T) {
	tests := []struct {
		name      string
		existing  string
		overwrite bool
		wantErr   bool
	}{
		{name: "new"},
		{name: "existing file", existing: "file", wantErr: true},
		{name: "existing directory", existing: "directory", wantErr: true},
		{name: "broken symlink", existing: "symlink", wantErr: true},
		{name: "directory symlink", existing: "directory symlink", wantErr: true},
		{name: "overwrite directory", existing: "directory", overwrite: true, wantErr: true},
		{name: "overwrite", existing: "file", overwrite: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "source")
			dest := filepath.Join(dir, "destination")
			if err := os.WriteFile(src, []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
			switch tt.existing {
			case "file":
				if err := os.WriteFile(dest, []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(dest, 0700); err != nil {
					t.Fatal(err)
				}
			case "directory symlink":
				target := filepath.Join(dir, "target")
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, dest); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(dir, "missing"), dest); err != nil {
					t.Fatal(err)
				}
			}
			helper := filepath.Join(dir, "copy-helper")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nif [ \"$1\" = -copy-publish-capable ]; then exit 0; fi\nexport COVE_COPY_TEST_STAGE=\"$2\" COVE_COPY_TEST_DEST=\"$4\" COVE_COPY_TEST_OVERWRITE=\"$5\"\nexec \"" + executable + "\" -test.run=^TestGuestCopyPublishHelper$\n"
			if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("/bin/sh", "-c", guestCopyInstallScript, "test", dir, dest, src, "640", strconv.FormatBool(tt.overwrite), helper, filepath.Join(dir, ".cove-copy-stage")).CombinedOutput()
			if (err != nil) != tt.wantErr {
				t.Fatalf("install error = %v, want error %v: %s", err, tt.wantErr, out)
			}
			if tt.wantErr {
				switch tt.existing {
				case "file":
					data, err := os.ReadFile(dest)
					if err != nil || string(data) != "old" {
						t.Fatalf("existing file changed: %q %v", data, err)
					}
				case "directory":
					entries, err := os.ReadDir(dest)
					if err != nil || len(entries) != 0 {
						t.Fatalf("directory changed: %v %v", entries, err)
					}
				case "symlink", "directory symlink":
					if _, err := os.Readlink(dest); err != nil {
						t.Fatalf("symlink changed: %v", err)
					}
				}
			} else {
				data, err := os.ReadFile(dest)
				if err != nil || string(data) != "new" {
					t.Fatalf("copied contents: %q %v", data, err)
				}
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, ".cove-copy.*"))
			if err != nil || len(leftovers) > 0 {
				t.Fatalf("temporary files: %v %v", leftovers, err)
			}
		})
	}
}

func TestCopyRequiresUserAgent(t *testing.T) {
	oldLinux := linuxMode
	linuxMode = false
	defer func() { linuxMode = oldLinux }()
	s := &ControlServer{}
	src := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(src, []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, toGuest := range []bool{false, true} {
		response := s.handleAgentCopy(&controlpb.AgentCopyCommand{HostPath: src, GuestPath: "/Users/alice/Downloads/file", ToGuest: toGuest})
		if !strings.Contains(response.Error, "copy requires the guest user agent; log in to the guest and retry") {
			t.Fatalf("toGuest=%v: %v", toGuest, response)
		}
	}
	response := s.handleAgentCopyDir(context.Background(), nil, t.TempDir(), "/Users/alice/Downloads/folder", false)
	if !strings.Contains(response.Error, "copy requires the guest user agent") {
		t.Fatal(response)
	}
}

func TestGuestCopyPublishHelper(t *testing.T) {
	stage := os.Getenv("COVE_COPY_TEST_STAGE")
	if stage == "" {
		return
	}
	if err := publishCopy(stage, os.Getenv("COVE_COPY_TEST_DEST"), os.Getenv("COVE_COPY_TEST_OVERWRITE") == "-copy-overwrite"); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
