package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunWorkerChildEnvEnablesSandboxMacgo(t *testing.T) {
	got := runWorkerChildEnv([]string{
		"PATH=/bin",
		"COVE_APP_SANDBOX_MACGO=0",
		"GOPATH=/tmp/go",
	})
	if strings.Join(got, "\n") != "PATH=/bin\nGOPATH=/tmp/go\nCOVE_APP_SANDBOX_MACGO=1" {
		t.Fatalf("runWorkerChildEnv() = %q", got)
	}
}

func TestFirstJSONObjectBytes(t *testing.T) {
	got, err := firstJSONObjectBytes([]byte("warning\n{\"ok\":true}\ntrailer\n"))
	if err != nil {
		t.Fatalf("firstJSONObjectBytes: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("firstJSONObjectBytes = %q", got)
	}
}

func TestRunWorkerHandoffRoundTrip(t *testing.T) {
	payload := []byte("run-worker handoff descriptor\n")
	sum := sha256.Sum256(payload)
	want := hex.EncodeToString(sum[:])
	handoff := runWorkerHandoff{
		Version: runWorkerHandoffVersion,
		Command: "probe",
		VM:      runWorkerHandoffVM{Name: "vm", Dir: "/tmp/vm"},
		FDs: []runWorkerHandoffFD{{
			Name:   "grant",
			Index:  0,
			Path:   "/tmp/grant",
			Mode:   "read",
			SHA256: want,
		}},
		Bookmarks: []runWorkerHandoffBookmark{{
			Key:   "vm:vm",
			Kind:  "vm",
			Path:  "/tmp/vm",
			Bytes: []byte("bookmark"),
		}},
	}
	data, err := encodeRunWorkerHandoff(handoff)
	if err != nil {
		t.Fatalf("encodeRunWorkerHandoff: %v", err)
	}
	got, err := decodeRunWorkerHandoff(data)
	if err != nil {
		t.Fatalf("decodeRunWorkerHandoff: %v", err)
	}
	if got.Command != "probe" || got.VM.Name != "vm" || len(got.FDs) != 1 || len(got.Bookmarks[0].Bytes) == 0 {
		t.Fatalf("decodeRunWorkerHandoff = %+v", got)
	}
}

func TestRunWorkerHandoffSocketCarriesDescriptor(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "cove-rw-")
	if err != nil {
		t.Fatalf("create short socket dir: %v", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "grant.txt")
	payload := []byte("run-worker handoff fd\n")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write grant: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open grant: %v", err)
	}
	defer file.Close()

	sum := sha256.Sum256(payload)
	handoff := runWorkerHandoff{
		Version: runWorkerHandoffVersion,
		Command: "probe",
		VM:      runWorkerHandoffVM{Name: "vm", Dir: dir},
		FDs: []runWorkerHandoffFD{{
			Name:   "grant",
			Index:  0,
			Path:   path,
			Mode:   "read",
			SHA256: hex.EncodeToString(sum[:]),
		}},
	}
	sockPath := filepath.Join(dir, "rw.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	errc := make(chan error, 1)
	go func() {
		errc <- sendRunWorkerHandoff(ln, handoff, []*os.File{file}, time.Second)
	}()
	got, files, err := receiveRunWorkerHandoff(sockPath, time.Second)
	if err != nil {
		t.Fatalf("receiveRunWorkerHandoff: %v", err)
	}
	defer closeRunWorkerFiles(files)
	if err := <-errc; err != nil {
		t.Fatalf("sendRunWorkerHandoff: %v", err)
	}
	if got.Command != handoff.Command || got.VM.Name != handoff.VM.Name {
		t.Fatalf("handoff = %+v, want %+v", got, handoff)
	}
	if len(files) != 1 {
		t.Fatalf("received %d files, want 1", len(files))
	}
	body, err := io.ReadAll(files[0])
	if err != nil {
		t.Fatalf("read received descriptor: %v", err)
	}
	if string(body) != string(payload) {
		t.Fatalf("descriptor payload = %q, want %q", body, payload)
	}
}

func TestRunWorkerHandoffSocketWithoutDescriptor(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "cove-rw-")
	if err != nil {
		t.Fatalf("create short socket dir: %v", err)
	}
	defer os.RemoveAll(dir)
	handoff := runWorkerHandoff{
		Version: runWorkerHandoffVersion,
		Command: "status-preflight",
		VM:      runWorkerHandoffVM{Name: "vm", Dir: dir},
		Bookmarks: []runWorkerHandoffBookmark{{
			Key:   "vm:vm",
			Kind:  "vm",
			Path:  dir,
			Bytes: []byte("bookmark"),
		}},
	}
	sockPath := filepath.Join(dir, "rw.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	errc := make(chan error, 1)
	go func() {
		errc <- sendRunWorkerHandoff(ln, handoff, nil, time.Second)
	}()
	got, files, err := receiveRunWorkerHandoff(sockPath, time.Second)
	if err != nil {
		t.Fatalf("receiveRunWorkerHandoff: %v", err)
	}
	defer closeRunWorkerFiles(files)
	if err := <-errc; err != nil {
		t.Fatalf("sendRunWorkerHandoff: %v", err)
	}
	if got.Command != "status-preflight" || got.VM.Name != "vm" {
		t.Fatalf("handoff = %+v", got)
	}
	if len(files) != 0 {
		t.Fatalf("received %d files, want 0", len(files))
	}
}
