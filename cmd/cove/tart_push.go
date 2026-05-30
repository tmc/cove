package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/vmconfig"
)

const tartDiskChunkSize = 512 << 20

type tartPushPlan struct {
	VMName         string
	VMDir          string
	Ref            string
	DiskPath       string
	DiskSize       int64
	ChunkSize      int64
	VMConfig       tartVMConfigOut
	VMConfigJSON   []byte
	OCIConfigJSON  []byte
	NvramPath      string
	NvramDigest    string
	NvramSize      int64
	DiskLayers     []tartPushDiskLayer
	Manifest       ociimage.Manifest
	UploadTime     string
	CompressedSize int64
}

type tartPushDiskLayer struct {
	Index                int
	Offset               int64
	UncompressedSize     int64
	UncompressedDigest   string
	CompressedSize       int64
	CompressedDigest     string
	CompressedDescriptor ociimage.Descriptor
}

type tartVMConfigOut struct {
	Version       int               `json:"version"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	ECID          string            `json:"ecid"`
	HardwareModel string            `json:"hardwareModel"`
	CPUCountMin   int               `json:"cpuCountMin"`
	CPUCount      int               `json:"cpuCount"`
	MemorySizeMin uint64            `json:"memorySizeMin"`
	MemorySize    uint64            `json:"memorySize"`
	MACAddress    string            `json:"macAddress"`
	Display       tartDisplayConfig `json:"display"`
	DiskFormat    string            `json:"diskFormat"`
}

type tartDisplayConfig struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type tartOCIConfigOut struct {
	Architecture string                 `json:"architecture"`
	OS           string                 `json:"os"`
	Config       tartOCIConfigContainer `json:"config"`
}

type tartOCIConfigContainer struct {
	Labels map[string]string `json:"Labels,omitempty"`
}

func buildTartPushPlan(vmName, ref string, opts pushOptions) (*tartPushPlan, error) {
	if err := validatePushReferences(ref, opts); err != nil {
		return nil, err
	}
	if opts.BaseRef != "" {
		return nil, fmt.Errorf("cove push --format tart does not support --base")
	}
	if opts.LumeCompat {
		return nil, fmt.Errorf("cove push --format tart does not support --lume-compat")
	}
	if opts.ChunkSize != 0 && opts.ChunkSize != tartDiskChunkSize {
		return nil, fmt.Errorf("cove push --format tart requires 512 MiB disk layers")
	}
	vmDir := pushSourceDir(vmName)
	if !vmconfig.Validate(vmDir) {
		return nil, fmt.Errorf("vm not found or invalid: %s", vmDir)
	}
	if err := ensurePushSourceInactive(vmDir); err != nil {
		return nil, err
	}
	diskPath := filepath.Join(vmDir, "disk.img")
	info, err := os.Stat(diskPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("tart push requires macOS disk.img in %s", vmDir)
		}
		return nil, fmt.Errorf("stat disk: %w", err)
	}

	cfg, configJSON, err := projectCoveToTart(vmDir)
	if err != nil {
		return nil, err
	}
	ociConfigJSON, err := json.Marshal(tartOCIConfigOut{
		Architecture: "arm64",
		OS:           "darwin",
		Config: tartOCIConfigContainer{Labels: map[string]string{
			"org.cirruslabs.tart.disk.format": "raw",
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal tart oci config: %w", err)
	}

	nvramPath := filepath.Join(vmDir, "aux.img")
	nvramBlob, err := ociimage.DigestFile(nvramPath)
	if err != nil {
		return nil, fmt.Errorf("digest nvram: %w", err)
	}
	layers, compressedSize, err := planTartDiskLayers(diskPath, tartDiskChunkSize)
	if err != nil {
		return nil, err
	}
	uploadTime := time.Now().UTC().Format(time.RFC3339)
	plan := &tartPushPlan{
		VMName:         vmName,
		VMDir:          vmDir,
		Ref:            ref,
		DiskPath:       diskPath,
		DiskSize:       info.Size(),
		ChunkSize:      tartDiskChunkSize,
		VMConfig:       cfg,
		VMConfigJSON:   configJSON,
		OCIConfigJSON:  ociConfigJSON,
		NvramPath:      nvramPath,
		NvramDigest:    nvramBlob.Digest,
		NvramSize:      nvramBlob.Size,
		DiskLayers:     layers,
		UploadTime:     uploadTime,
		CompressedSize: compressedSize,
	}
	plan.Manifest = buildTartManifest(plan)
	return plan, nil
}

func projectCoveToTart(vmDir string) (tartVMConfigOut, []byte, error) {
	cfg, err := vmconfig.Load(vmDir)
	if err != nil {
		return tartVMConfigOut{}, nil, err
	}
	hwModel, err := readRequiredFile(filepath.Join(vmDir, "hw.model"), "hw.model")
	if err != nil {
		return tartVMConfigOut{}, nil, err
	}
	machineID, err := readRequiredFile(filepath.Join(vmDir, "machine.id"), "machine.id")
	if err != nil {
		return tartVMConfigOut{}, nil, err
	}
	cpu := 4
	memory := uint64(4 << 30)
	if cfg != nil {
		if cfg.CPU > 0 {
			cpu = int(cfg.CPU)
		}
		if cfg.MemoryGB > 0 {
			memory = uint64(cfg.MemoryGB) << 30
		}
	}
	mac := tartMACAddress(vmDir, machineID)
	out := tartVMConfigOut{
		Version:       1,
		OS:            "darwin",
		Arch:          "arm64",
		ECID:          base64.StdEncoding.EncodeToString(machineID),
		HardwareModel: base64.StdEncoding.EncodeToString(hwModel),
		CPUCountMin:   cpu,
		CPUCount:      cpu,
		MemorySizeMin: memory,
		MemorySize:    memory,
		MACAddress:    mac,
		Display:       tartDisplayConfig{Width: 1024, Height: 768},
		DiskFormat:    "raw",
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return tartVMConfigOut{}, nil, fmt.Errorf("marshal tart config: %w", err)
	}
	return out, append(data, '\n'), nil
}

func readRequiredFile(path, name string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("read %s: empty file", name)
	}
	return data, nil
}

func tartMACAddress(vmDir string, seed []byte) string {
	if data, err := os.ReadFile(filepath.Join(vmDir, "mac.address")); err == nil {
		mac := strings.TrimSpace(string(data))
		if _, err := net.ParseMAC(mac); err == nil {
			return mac
		}
	}
	sum := sha256.Sum256(seed)
	b := []byte{0x02, sum[0], sum[1], sum[2], sum[3], sum[4]}
	return net.HardwareAddr(b).String()
}

func planTartDiskLayers(diskPath string, chunkSize int64) ([]tartPushDiskLayer, int64, error) {
	f, err := os.Open(diskPath)
	if err != nil {
		return nil, 0, fmt.Errorf("open disk: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("stat disk: %w", err)
	}
	var layers []tartPushDiskLayer
	var compressedTotal int64
	for offset, index := int64(0), 0; offset < info.Size(); offset, index = offset+chunkSize, index+1 {
		size := chunkSize
		if remain := info.Size() - offset; remain < size {
			size = remain
		}
		layer, err := buildTartDiskLayer(f, index, offset, size)
		if err != nil {
			return nil, 0, err
		}
		layers = append(layers, layer)
		compressedTotal += layer.CompressedSize
	}
	if len(layers) == 0 {
		return nil, 0, fmt.Errorf("empty disk image")
	}
	return layers, compressedTotal, nil
}

func buildTartDiskLayer(r io.ReaderAt, index int, offset, size int64) (tartPushDiskLayer, error) {
	raw, err := readAtBytes(r, offset, size)
	if err != nil {
		return tartPushDiskLayer{}, fmt.Errorf("read tart disk layer %d: %w", index, err)
	}
	rawDigest := digestBytes(raw)
	compressed, err := ociimage.CompressAppleLZ4(raw)
	if err != nil {
		return tartPushDiskLayer{}, fmt.Errorf("compress tart disk layer %d: %w", index, err)
	}
	compressedDigest := digestBytes(compressed)
	desc := ociimage.Descriptor{
		MediaType: ociimage.TartDiskV2MediaType,
		Size:      int64(len(compressed)),
		Digest:    compressedDigest,
		Annotations: map[string]string{
			ociimage.TartUncompressedSize:          strconv.FormatInt(size, 10),
			ociimage.TartUncompressedContentDigest: rawDigest,
		},
	}
	return tartPushDiskLayer{
		Index:                index,
		Offset:               offset,
		UncompressedSize:     size,
		UncompressedDigest:   rawDigest,
		CompressedSize:       int64(len(compressed)),
		CompressedDigest:     compressedDigest,
		CompressedDescriptor: desc,
	}, nil
}

func readAtBytes(r io.ReaderAt, offset, size int64) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("negative size %d", size)
	}
	if size > int64(int(size)) {
		return nil, fmt.Errorf("size %d overflows int", size)
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(io.NewSectionReader(r, offset, size), data); err != nil {
		return nil, err
	}
	return data, nil
}

func buildTartManifest(plan *tartPushPlan) ociimage.Manifest {
	configDesc := ociimage.Descriptor{
		MediaType: ociimage.TartConfigMediaType,
		Size:      int64(len(plan.VMConfigJSON)),
		Digest:    digestBytes(plan.VMConfigJSON),
	}
	layers := make([]ociimage.Descriptor, 0, len(plan.DiskLayers)+2)
	layers = append(layers, configDesc)
	for _, layer := range plan.DiskLayers {
		layers = append(layers, layer.CompressedDescriptor)
	}
	layers = append(layers, ociimage.Descriptor{
		MediaType: ociimage.TartNVRAMMediaType,
		Size:      plan.NvramSize,
		Digest:    plan.NvramDigest,
	})
	return ociimage.Manifest{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageManifest,
		Config: ociimage.Descriptor{
			MediaType: ociimage.MediaTypeImageConfig,
			Size:      int64(len(plan.OCIConfigJSON)),
			Digest:    digestBytes(plan.OCIConfigJSON),
		},
		Layers: layers,
		Annotations: map[string]string{
			ociimage.TartUncompressedDiskSize: strconv.FormatInt(plan.DiskSize, 10),
			ociimage.TartUploadTime:           plan.UploadTime,
		},
	}
}

func runTartPush(ctx context.Context, plan *tartPushPlan, opts pushOptions) error {
	if !opts.DryRun {
		if err := pushTartImage(ctx, plan, opts); err != nil {
			return err
		}
		if opts.ManifestOut != "" {
			if err := writePushManifest(opts.ManifestOut, plan.Manifest); err != nil {
				return err
			}
		}
		printTartPushResult(os.Stdout, plan, opts)
		return nil
	}
	if opts.ManifestOut != "" {
		if err := writePushManifest(opts.ManifestOut, plan.Manifest); err != nil {
			return err
		}
	}
	printTartPushDryRun(os.Stdout, plan)
	return nil
}

func pushTartImage(ctx context.Context, plan *tartPushPlan, opts pushOptions) error {
	ref, err := ociimage.ParseReference(plan.Ref)
	if err != nil {
		return fmt.Errorf("cove push: invalid target ref %q: %w", plan.Ref, err)
	}
	client := pushRegistryClient(ref, opts)
	if err := uploadBytesBlob(ctx, client, ref, plan.Manifest.Config, plan.OCIConfigJSON); err != nil {
		return err
	}
	configDesc := plan.Manifest.Layers[0]
	if err := uploadBytesBlob(ctx, client, ref, configDesc, plan.VMConfigJSON); err != nil {
		return err
	}
	disk, err := os.Open(plan.DiskPath)
	if err != nil {
		return fmt.Errorf("open disk: %w", err)
	}
	defer disk.Close()
	for _, layer := range plan.DiskLayers {
		data, err := tartDiskLayerData(disk, layer)
		if err != nil {
			return err
		}
		if err := uploadBytesBlob(ctx, client, ref, layer.CompressedDescriptor, data); err != nil {
			return err
		}
	}
	nvramDesc := plan.Manifest.Layers[len(plan.Manifest.Layers)-1]
	if err := uploadFileBlob(ctx, client, ref, nvramDesc, plan.NvramPath); err != nil {
		return err
	}
	if _, err := client.PushManifest(ctx, ref, plan.Manifest); err != nil {
		return err
	}
	for _, tag := range opts.AdditionalTags {
		extra := ref
		extra.Tag = tag
		if _, err := client.PushManifest(ctx, extra, plan.Manifest); err != nil {
			return err
		}
	}
	return nil
}

func tartDiskLayerData(r io.ReaderAt, layer tartPushDiskLayer) ([]byte, error) {
	raw, err := readAtBytes(r, layer.Offset, layer.UncompressedSize)
	if err != nil {
		return nil, fmt.Errorf("read tart disk layer %d: %w", layer.Index, err)
	}
	if got := digestBytes(raw); got != layer.UncompressedDigest {
		return nil, fmt.Errorf("tart disk layer %d content digest %s, want %s", layer.Index, got, layer.UncompressedDigest)
	}
	compressed, err := ociimage.CompressAppleLZ4(raw)
	if err != nil {
		return nil, fmt.Errorf("compress tart disk layer %d: %w", layer.Index, err)
	}
	if got := digestBytes(compressed); got != layer.CompressedDigest {
		return nil, fmt.Errorf("tart disk layer %d compressed digest %s, want %s", layer.Index, got, layer.CompressedDigest)
	}
	return compressed, nil
}

func printTartPushDryRun(w io.Writer, plan *tartPushPlan) {
	fmt.Fprintln(w, "Push dry run (tart format)")
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref)
	fmt.Fprintf(w, "  disk: %s (%s)\n", plan.DiskPath, bytefmt.Size(plan.DiskSize))
	fmt.Fprintf(w, "  chunk size: %s\n", bytefmt.Size(plan.ChunkSize))
	fmt.Fprintf(w, "  disk layers: %d (compressed %s)\n", len(plan.DiskLayers), bytefmt.Size(plan.CompressedSize))
	fmt.Fprintf(w, "  config: %d B\n", len(plan.VMConfigJSON))
	fmt.Fprintf(w, "  nvram:  %s\n", bytefmt.Size(plan.NvramSize))
}

func printTartPushResult(w io.Writer, plan *tartPushPlan, opts pushOptions) {
	fmt.Fprintln(w, "Push complete (tart format)")
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref)
	fmt.Fprintf(w, "  disk layers: %d (compressed %s)\n", len(plan.DiskLayers), bytefmt.Size(plan.CompressedSize))
	if len(opts.AdditionalTags) > 0 {
		fmt.Fprintf(w, "  additional tags: %s\n", strings.Join(opts.AdditionalTags, ", "))
	}
}
