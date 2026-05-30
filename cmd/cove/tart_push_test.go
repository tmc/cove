package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestBuildTartPushPlanManifestShape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	disk := []byte("hello tart disk")
	vmPath := stageTartPushVM(t, "dev-vm", disk)
	if err := os.WriteFile(filepath.Join(vmPath, "mac.address"), []byte("aa:bb:cc:dd:ee:ff\n"), 0644); err != nil {
		t.Fatalf("WriteFile(mac.address) error = %v", err)
	}
	if err := vmconfig.Save(vmPath, &vmconfig.Config{CPU: 6, MemoryGB: 8}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	plan, err := buildTartPushPlan("dev-vm", "ghcr.io/me/dev-vm:v1", pushOptions{})
	if err != nil {
		t.Fatalf("buildTartPushPlan(): %v", err)
	}
	parsed, err := ociimage.ParseManifest(plan.Manifest)
	if err != nil {
		t.Fatalf("ParseManifest(): %v", err)
	}
	if parsed.Format != ociimage.FormatTart {
		t.Fatalf("Format = %s, want tart", parsed.Format)
	}
	if got, want := len(parsed.Tart.DiskLayers), 1; got != want {
		t.Fatalf("DiskLayers = %d, want %d", got, want)
	}
	if parsed.Tart.UncompressedDiskSize != int64(len(disk)) {
		t.Fatalf("UncompressedDiskSize = %d, want %d", parsed.Tart.UncompressedDiskSize, len(disk))
	}
	if plan.VMConfig.CPUCount != 6 || plan.VMConfig.MemorySize != 8<<30 {
		t.Fatalf("VMConfig resources = cpu %d memory %d", plan.VMConfig.CPUCount, plan.VMConfig.MemorySize)
	}
	if plan.VMConfig.MACAddress != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("MACAddress = %q", plan.VMConfig.MACAddress)
	}
	if plan.VMConfig.HardwareModel != base64.StdEncoding.EncodeToString([]byte("hw")) {
		t.Fatalf("HardwareModel = %q", plan.VMConfig.HardwareModel)
	}
	if plan.VMConfig.ECID != base64.StdEncoding.EncodeToString([]byte("machine")) {
		t.Fatalf("ECID = %q", plan.VMConfig.ECID)
	}
	var ociConfig struct {
		Config struct {
			Labels map[string]string
		}
	}
	if err := json.Unmarshal(plan.OCIConfigJSON, &ociConfig); err != nil {
		t.Fatalf("Unmarshal OCI config: %v", err)
	}
	if got := ociConfig.Config.Labels["org.cirruslabs.tart.disk.format"]; got != "raw" {
		t.Fatalf("disk format label = %q, want raw", got)
	}
}

func TestBuildTartPushPlanRejectsUnsupportedOptions(t *testing.T) {
	tests := []struct {
		name string
		opts pushOptions
		want string
	}{
		{name: "base", opts: pushOptions{BaseRef: "ghcr.io/me/base:v1"}, want: "does not support --base"},
		{name: "lume compat", opts: pushOptions{LumeCompat: true}, want: "does not support --lume-compat"},
		{name: "chunk size", opts: pushOptions{ChunkSize: 128 << 20}, want: "requires 512 MiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildTartPushPlan("dev-vm", "ghcr.io/me/dev-vm:v1", tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("buildTartPushPlan() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunTartPushDryRunWritesManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	plan := &tartPushPlan{
		VMName:   "dev-vm",
		Ref:      "ghcr.io/me/dev-vm:v1",
		DiskPath: "/dev/null",
		Manifest: ociimage.Manifest{SchemaVersion: 2},
	}
	oldStdout := os.Stdout
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	os.Stdout = devnull
	defer func() {
		os.Stdout = oldStdout
		devnull.Close()
	}()
	if err := runTartPush(context.Background(), plan, pushOptions{DryRun: true, ManifestOut: manifestPath}); err != nil {
		t.Fatalf("runTartPush(): %v", err)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
}

func TestRunTartPushUploadsRegistryContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	disk := bytes.Repeat([]byte("tart-disk-data"), 64)
	stageTartPushVM(t, "dev-vm", disk)

	uploaded := map[string][]byte{}
	manifests := map[string]ociimage.Manifest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const blobPrefix = "/v2/me/dev-vm/blobs/"
		const uploadPrefix = "/v2/me/dev-vm/blobs/uploads/"
		const manifestPrefix = "/v2/me/dev-vm/manifests/"
		switch {
		case r.Method == http.MethodHead && strings.HasPrefix(r.URL.Path, blobPrefix):
			digest := strings.TrimPrefix(r.URL.Path, blobPrefix)
			if _, ok := uploaded[digest]; ok {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == uploadPrefix:
			w.Header().Set("Location", uploadPrefix+"upload-id")
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && r.URL.Path == uploadPrefix+"upload-id":
			digest := r.URL.Query().Get("digest")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			if got := pushTestDigest(data); got != digest {
				t.Fatalf("uploaded digest = %q, want %q", got, digest)
			}
			uploaded[digest] = data
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, manifestPrefix):
			tag := strings.TrimPrefix(r.URL.Path, manifestPrefix)
			var manifest ociimage.Manifest
			if err := json.NewDecoder(r.Body).Decode(&manifest); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			manifests[tag] = manifest
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("%s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	opts := pushOptions{
		AdditionalTags:  stringList{"latest"},
		ManifestOut:     manifestPath,
		RegistryBaseURL: srv.URL,
	}
	plan, err := buildTartPushPlan("dev-vm", "ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildTartPushPlan(): %v", err)
	}
	out, err := captureStdoutResult(t, func() error {
		return runTartPush(context.Background(), plan, opts)
	})
	if err != nil {
		t.Fatalf("runTartPush(): %v", err)
	}
	if !strings.Contains(out, "Push complete (tart format)") {
		t.Fatalf("output %q missing completion line", out)
	}
	if _, ok := manifests["v1"]; !ok {
		t.Fatal("missing v1 manifest")
	}
	if _, ok := manifests["latest"]; !ok {
		t.Fatal("missing latest manifest")
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if _, ok := uploaded[plan.Manifest.Config.Digest]; !ok {
		t.Fatalf("OCI config digest %s was not uploaded", plan.Manifest.Config.Digest)
	}
	for _, layer := range plan.Manifest.Layers {
		if _, ok := uploaded[layer.Digest]; !ok {
			t.Fatalf("layer digest %s was not uploaded", layer.Digest)
		}
	}
	gotDisk, err := ociimage.DecompressAppleLZ4(uploaded[plan.DiskLayers[0].CompressedDigest])
	if err != nil {
		t.Fatalf("DecompressAppleLZ4(): %v", err)
	}
	if !bytes.Equal(gotDisk, disk) {
		t.Fatalf("decompressed disk = %d bytes, want %d", len(gotDisk), len(disk))
	}
}

func stageTartPushVM(t *testing.T, name string, disk []byte) string {
	t.Helper()
	vmPath := filepath.Join(vmconfig.BaseDir(), name)
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	files := map[string][]byte{
		"disk.img":   disk,
		"aux.img":    []byte("aux"),
		"hw.model":   []byte("hw"),
		"machine.id": []byte("machine"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(vmPath, name), data, 0644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	return vmPath
}
