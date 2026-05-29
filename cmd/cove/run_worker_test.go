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

	"github.com/tmc/cove/internal/imagestore"
	"github.com/tmc/cove/internal/vmconfig"
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

func TestRunWorkerListVMRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"b", "a"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("mkdir VM: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0600); err != nil {
			t.Fatalf("write VM disk: %v", err)
		}
		if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 2}); err != nil {
			t.Fatalf("save VM config: %v", err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "not-a-vm"), 0700); err != nil {
		t.Fatalf("mkdir non-VM: %v", err)
	}
	vms, err := runWorkerListVMRoot(root)
	if err != nil {
		t.Fatalf("runWorkerListVMRoot: %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("listed %d VMs, want 2: %+v", len(vms), vms)
	}
	if vms[0].Name != "a" || vms[1].Name != "b" {
		t.Fatalf("VM order = %+v, want a,b", vms)
	}
	for _, vm := range vms {
		if vm.OSType != "Linux" || vm.State != "stopped" || !vm.ConfigRead {
			t.Fatalf("VM metadata = %+v, want Linux stopped with config", vm)
		}
	}
}

func TestRunWorkerListImageRoot(t *testing.T) {
	root := t.TempDir()
	created := time.Date(2026, 5, 27, 1, 2, 3, 0, time.UTC)
	for _, tt := range []struct {
		name string
		tag  string
	}{
		{name: "b/image", tag: "latest"},
		{name: "a-image", tag: "v1"},
	} {
		dir := filepath.Join(append([]string{root}, append(strings.Split(tt.name, "/"), tt.tag)...)...)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("mkdir image: %v", err)
		}
		if err := imagestore.WriteManifest(dir, &imagestore.Manifest{
			SchemaVersion: 1,
			Name:          tt.name,
			Tag:           tt.tag,
			SourceVM:      "source",
			DiskSize:      123,
			CreatedAt:     created,
		}); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}
	images, err := runWorkerListImageRoot(root)
	if err != nil {
		t.Fatalf("runWorkerListImageRoot: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("listed %d images, want 2: %+v", len(images), images)
	}
	if images[0].Ref != "a-image:v1" || images[1].Ref != "b/image:latest" {
		t.Fatalf("image order = %+v, want a-image:v1, b/image:latest", images)
	}
	for _, image := range images {
		if image.DiskSize != 123 || image.SourceVM != "source" || !image.ManifestRead || !image.CreatedAt.Equal(created) {
			t.Fatalf("image metadata = %+v", image)
		}
	}
}

func TestRunWorkerInspectImageRoots(t *testing.T) {
	imageRoot := t.TempDir()
	vmRoot := t.TempDir()
	ref := imagestore.Ref{Name: "base", Tag: "v1"}
	imageDir := filepath.Join(imageRoot, "base", "v1")
	if err := os.MkdirAll(imageDir, 0700); err != nil {
		t.Fatalf("mkdir image: %v", err)
	}
	created := time.Date(2026, 5, 27, 1, 2, 3, 0, time.UTC)
	if err := imagestore.WriteManifest(imageDir, &imagestore.Manifest{
		SchemaVersion: 1,
		Name:          ref.Name,
		Tag:           ref.Tag,
		SourceVM:      "source",
		DiskSize:      123,
		DiskSHA256:    strings.Repeat("a", 64),
		CreatedAt:     created,
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(imageDir, "hw.model"), []byte("hardware"), 0600); err != nil {
		t.Fatalf("write hw.model: %v", err)
	}
	for _, name := range []string{"fork-b", "fork-a"} {
		dir := filepath.Join(vmRoot, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("mkdir VM: %v", err)
		}
		if err := vmconfig.Save(dir, &vmconfig.Config{ParentImage: ref.String()}); err != nil {
			t.Fatalf("save VM config: %v", err)
		}
	}
	other := filepath.Join(vmRoot, "other")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatalf("mkdir other VM: %v", err)
	}
	if err := vmconfig.Save(other, &vmconfig.Config{ParentImage: "other:v1"}); err != nil {
		t.Fatalf("save other VM config: %v", err)
	}

	out, err := runWorkerInspectImageRoots(ref, imageRoot, vmRoot)
	if err != nil {
		t.Fatalf("runWorkerInspectImageRoots: %v", err)
	}
	if out.Ref != ref.String() || out.DiskSize != 123 || out.Created != created.Format(time.RFC3339) {
		t.Fatalf("inspect output = %+v", out)
	}
	if out.ForkCount != 2 || strings.Join(out.Forks, ",") != "fork-a,fork-b" {
		t.Fatalf("forks = %v count=%d, want fork-a,fork-b", out.Forks, out.ForkCount)
	}
	if out.MachineModelID == "" {
		t.Fatalf("MachineModelID is empty: %+v", out)
	}
}
