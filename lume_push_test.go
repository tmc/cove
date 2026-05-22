package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestLumePartTitle(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{1, "disk.img.part.aa"},
		{2, "disk.img.part.ab"},
		{26, "disk.img.part.az"},
		{27, "disk.img.part.ba"},
		{41, "disk.img.part.bo"}, // matches trycua/ubuntu-noble-vanilla:latest last part
		{52, "disk.img.part.bz"},
		{53, "disk.img.part.ca"},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("n=%d", tc.n), func(t *testing.T) {
			if got := lumePartTitle(tc.n); got != tc.want {
				t.Errorf("lumePartTitle(%d) = %q, want %q", tc.n, got, tc.want)
			}
		})
	}
	if got := lumePartTitle(0); got != "" {
		t.Errorf("lumePartTitle(0) = %q, want empty", got)
	}
}

func TestProjectCoveToLumeDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := projectCoveToLume(dir, 21474836480)
	if err != nil {
		t.Fatalf("projectCoveToLume: %v", err)
	}
	// No vmconfig.Config, no mac.address, no linux-disk.img → all defaults.
	if cfg.OS != "macos" {
		t.Errorf("OS = %q, want macos", cfg.OS)
	}
	if cfg.CPUCount != 4 {
		t.Errorf("CPUCount = %d, want 4", cfg.CPUCount)
	}
	if cfg.MemorySize != 4<<30 {
		t.Errorf("MemorySize = %d, want %d", cfg.MemorySize, uint64(4<<30))
	}
	if cfg.DiskSize != 21474836480 {
		t.Errorf("DiskSize = %d, want 21474836480", cfg.DiskSize)
	}
	if cfg.Display != "1024x768" {
		t.Errorf("Display = %q, want 1024x768", cfg.Display)
	}
	if cfg.MACAddress != "" {
		t.Errorf("MACAddress = %q, want empty", cfg.MACAddress)
	}
}

func TestProjectCoveToLumeUsesConfigAndMAC(t *testing.T) {
	dir := t.TempDir()
	if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 8, MemoryGB: 16}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mac.address"), []byte("aa:bb:cc:dd:ee:ff\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := projectCoveToLume(dir, 1<<30)
	if err != nil {
		t.Fatalf("projectCoveToLume: %v", err)
	}
	if cfg.OS != "linux" {
		t.Errorf("OS = %q, want linux", cfg.OS)
	}
	if cfg.CPUCount != 8 {
		t.Errorf("CPUCount = %d, want 8", cfg.CPUCount)
	}
	if cfg.MemorySize != 16<<30 {
		t.Errorf("MemorySize = %d, want %d", cfg.MemorySize, uint64(16<<30))
	}
	if cfg.MACAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MACAddress = %q, want aa:bb:cc:dd:ee:ff", cfg.MACAddress)
	}
}

func TestProjectCoveToLumeRejectsBadMAC(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mac.address"), []byte("not-a-mac\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := projectCoveToLume(dir, 1<<30)
	if err != nil {
		t.Fatalf("projectCoveToLume: %v", err)
	}
	if cfg.MACAddress != "" {
		t.Errorf("MACAddress = %q, want empty (invalid input rejected)", cfg.MACAddress)
	}
}

func TestPlanLumeDiskPartsSplitsAndDigestsAreReadable(t *testing.T) {
	// 3 MiB of repeating bytes — gzipped tar of this won't be exactly 3 MiB,
	// but the math should still produce ceil(N / chunkSize) parts.
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.img")
	payload := make([]byte, 3<<20)
	for i := range payload {
		payload[i] = byte(i & 0xff)
	}
	if err := os.WriteFile(disk, payload, 0644); err != nil {
		t.Fatal(err)
	}
	parts, total, err := planLumeDiskParts(disk, 1<<20) // 1 MiB chunks
	if err != nil {
		t.Fatalf("planLumeDiskParts: %v", err)
	}
	if len(parts) == 0 {
		t.Fatal("no parts produced")
	}
	var sum int64
	for i, p := range parts {
		want := int64(1 << 20)
		if i == len(parts)-1 {
			want = total - int64(i)*int64(1<<20)
		}
		if p.Size != want {
			t.Errorf("part %d size = %d, want %d", i+1, p.Size, want)
		}
		if p.Number != i+1 {
			t.Errorf("part %d number = %d, want %d", i+1, p.Number, i+1)
		}
		if !strings.HasPrefix(p.Title, "disk.img.part.") {
			t.Errorf("part %d title = %q, want disk.img.part.*", i+1, p.Title)
		}
		if !strings.Contains(p.MediaType, "part.number=") {
			t.Errorf("part %d mediaType missing part.number: %q", i+1, p.MediaType)
		}
		if !strings.HasPrefix(p.Digest, "sha256:") {
			t.Errorf("part %d digest = %q, want sha256: prefix", i+1, p.Digest)
		}
		sum += p.Size
	}
	if sum != total {
		t.Errorf("sum of parts = %d, want %d", sum, total)
	}
}

func TestWriteTarGzipStreamReversible(t *testing.T) {
	// Build a temp disk, tar+gzip it via writeTarGzipStream, then read it
	// back through gzip+tar and confirm the body matches. This is the
	// reverse-trip sanity check for the export — same library code path
	// the import uses to consume lume images.
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.img")
	payload := []byte("hello lume world — this is a tiny disk image")
	if err := os.WriteFile(disk, payload, 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "stream.tar.gz")
	w, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTarGzipStream(w, disk); err != nil {
		t.Fatalf("writeTarGzipStream: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()
	tarReader := tarReadOnly(gz)
	body, err := io.ReadAll(tarReader)
	if err != nil {
		t.Fatalf("tar read: %v", err)
	}
	if string(body) != string(payload) {
		t.Errorf("body mismatch: got %q, want %q", body, payload)
	}
}

// tarReadOnly opens the first regular file from the tar stream.
func tarReadOnly(r io.Reader) io.Reader {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return strings.NewReader("")
		}
		if hdr.Typeflag == tar.TypeReg {
			return tr
		}
	}
}

func TestBuildLumeManifestMatchesLumeShape(t *testing.T) {
	plan := &lumePushPlan{
		ConfigJSON:  []byte(`{"os":"linux","cpuCount":4,"memorySize":4294967296,"diskSize":1073741824}`),
		NvramSize:   131072,
		NvramDigest: "sha256:" + strings.Repeat("a", 64),
		UploadTime:  "2026-04-25T07:00:00Z",
		Parts: []lumePushPart{
			{Number: 1, Title: "disk.img.part.aa", Size: 100, Digest: "sha256:" + strings.Repeat("b", 64),
				MediaType: ociimage.LumeTarLayerMediaTypePrefix + ";part.number=1;part.total=2"},
			{Number: 2, Title: "disk.img.part.ab", Size: 50, Digest: "sha256:" + strings.Repeat("c", 64),
				MediaType: ociimage.LumeTarLayerMediaTypePrefix + ";part.number=2;part.total=2"},
		},
	}
	m := buildLumeManifest(plan, plan.ConfigJSON)
	if m.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", m.SchemaVersion)
	}
	if m.MediaType != ociimage.MediaTypeImageManifest {
		t.Errorf("MediaType = %q, want %q", m.MediaType, ociimage.MediaTypeImageManifest)
	}
	if m.Config.MediaType != "application/vnd.oci.empty.v1+json" {
		t.Errorf("Config.MediaType = %q, want application/vnd.oci.empty.v1+json", m.Config.MediaType)
	}
	if m.Config.Size != 2 {
		t.Errorf("Config.Size = %d, want 2", m.Config.Size)
	}
	if !strings.HasPrefix(m.Config.Digest, "sha256:") {
		t.Errorf("Config.Digest = %q, want sha256: prefix", m.Config.Digest)
	}
	if m.Annotations["org.opencontainers.image.created"] != "2026-04-25T07:00:00Z" {
		t.Errorf("created annotation = %q", m.Annotations["org.opencontainers.image.created"])
	}
	// Layers: 2 parts + config.json + nvram.bin = 4
	if len(m.Layers) != 4 {
		t.Fatalf("len(Layers) = %d, want 4", len(m.Layers))
	}
	if m.Layers[0].Annotations["org.opencontainers.image.title"] != "disk.img.part.aa" {
		t.Errorf("layer 0 title = %q", m.Layers[0].Annotations["org.opencontainers.image.title"])
	}
	if !strings.HasPrefix(m.Layers[0].MediaType, ociimage.LumeTarLayerMediaTypePrefix) {
		t.Errorf("layer 0 mediaType = %q", m.Layers[0].MediaType)
	}
	configLayer := m.Layers[2]
	if configLayer.Annotations["org.opencontainers.image.title"] != ociimage.LumeConfigTitle {
		t.Errorf("config layer title = %q, want %q", configLayer.Annotations["org.opencontainers.image.title"], ociimage.LumeConfigTitle)
	}
	if configLayer.MediaType != ociimage.MediaTypeImageConfig {
		t.Errorf("config layer mediaType = %q", configLayer.MediaType)
	}
	nvramLayer := m.Layers[3]
	if nvramLayer.Annotations["org.opencontainers.image.title"] != ociimage.LumeNvramTitle {
		t.Errorf("nvram layer title = %q", nvramLayer.Annotations["org.opencontainers.image.title"])
	}
}

func TestBuildLumeManifestRoundTripsThroughParser(t *testing.T) {
	// Reverse-trip sanity check: build a manifest with our exporter, then
	// run it through the importer's IsLumeManifest + ParseLumeManifest.
	// The exporter's output should be identifiable by our existing import
	// code as a lume manifest (= we're producing what we already consume).
	plan := &lumePushPlan{
		ConfigJSON:  []byte(`{"os":"linux"}`),
		NvramSize:   1024,
		NvramDigest: "sha256:" + strings.Repeat("d", 64),
		Parts: []lumePushPart{
			{Number: 1, Title: "disk.img.part.aa", Size: 100, Digest: "sha256:" + strings.Repeat("e", 64),
				MediaType: ociimage.LumeTarLayerMediaTypePrefix + ";part.number=1;part.total=1"},
		},
	}
	m := buildLumeManifest(plan, plan.ConfigJSON)
	if !ociimage.IsLumeManifest(m) {
		t.Fatal("IsLumeManifest = false; export should be importable")
	}
	parsed, err := ociimage.ParseLumeManifest(m)
	if err != nil {
		t.Fatalf("ParseLumeManifest: %v", err)
	}
	if len(parsed.DiskParts) != 1 {
		t.Errorf("DiskParts = %d, want 1", len(parsed.DiskParts))
	}
	if parsed.NvramLayer == nil {
		t.Error("NvramLayer = nil; expected nvram.bin layer")
	}
	if parsed.ConfigLayer == nil {
		t.Error("ConfigLayer = nil; expected config.json layer")
	}
}

func TestWriteLumeManifestOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	m := ociimage.Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json"}

	if err := writeLumeManifestOut(path, m); err != nil {
		t.Fatalf("writeLumeManifestOut: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := data[len(data)-1]; got != '\n' {
		t.Errorf("manifest does not end with newline; last byte = %q", got)
	}
	var round ociimage.Manifest
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", round.SchemaVersion)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Errorf("manifest perm = %04o, want 0644", perm)
	}
}

func TestWriteLumeManifestOutWriteError(t *testing.T) {
	dir := t.TempDir()
	// Path under a regular file → write fails.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bad := filepath.Join(blocker, "manifest.json")
	if err := writeLumeManifestOut(bad, ociimage.Manifest{}); err == nil {
		t.Fatal("expected write error")
	}
}

func TestLumePushDryRunOnlyRejectsNonDryRun(t *testing.T) {
	plan := &lumePushPlan{VMName: "x", Ref: "r"}
	err := lumePushDryRunOnly(plan, pushOptions{DryRun: false})
	if err == nil {
		t.Fatal("expected error for non-dry-run")
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("error %q does not mention --dry-run", err.Error())
	}
}

func TestLumePushDryRunOnlyWritesManifest(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "out.json")
	plan := &lumePushPlan{
		VMName:   "myvm",
		Ref:      "ghcr.io/foo/bar:tag",
		DiskPath: "/dev/null",
		Manifest: ociimage.Manifest{SchemaVersion: 2},
		Config:   lumeConfigOut{OS: "macos", CPUCount: 4, MemorySize: 4 << 30, DiskSize: 64 << 30},
	}
	// Redirect stdout to discard noise from printLumePushDryRun.
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

	if err := lumePushDryRunOnly(plan, pushOptions{DryRun: true, ManifestOut: manifestPath}); err != nil {
		t.Fatalf("lumePushDryRunOnly: %v", err)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
}

func TestLumeConfigOutMatchesObservedSchema(t *testing.T) {
	// Sanity: the JSON we serialize should look like trycua's published
	// schema. Field names verified against
	// ghcr.io/trycua/ubuntu-noble-vanilla:latest's config.json blob.
	cfg := lumeConfigOut{
		OS: "linux", CPUCount: 4, MemorySize: 4294967296,
		DiskSize: 21474836480, Display: "1024x768", MACAddress: "5e:78:38:77:8c:79",
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(data)
	for _, want := range []string{`"os":"linux"`, `"cpuCount":4`, `"memorySize":4294967296`,
		`"diskSize":21474836480`, `"display":"1024x768"`, `"macAddress":"5e:78:38:77:8c:79"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("config JSON missing %s; got %s", want, got)
		}
	}
}
