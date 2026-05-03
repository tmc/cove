// image.go — local VM image store (Slice 1 of design 024).
//
// An image is a content-addressed snapshot of a stopped VM bundle that
// can be forked into a fresh VM with `cove run -fork-from <image-ref>`.
// Slice 1 stays purely local: no registry, no push/pull, no signing,
// no network code. The on-disk shape mirrors a tiny OCI-style layout
// (repo/tag) so Slice 2 can later wrap it in real OCI artifacts.
//
// Layout: ~/.vz/images/<name>/<tag>/
//
//	manifest.json    — schema below
//	disk.img         — APFS clonefile of source VM disk (sparse-preserving)
//	aux.img          — byte copy of source VM aux storage
//	hw.model         — byte copy of source VM hardware model
//	machine.id       — byte copy of source VM machine identifier
//
// suspend.vmstate is intentionally excluded per design 024: vmstate
// binds to {machine.id, aux, MAC, disk} and does not survive identity
// rotation. Cold-boot only in Slice 1.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// ImagesBaseDir returns the local image store root. Mirrors vmconfig.BaseDir
// convention so a single ~/.vz tree holds VMs and images.
func ImagesBaseDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "images")
}

// ImageManifest is the on-disk schema for an image's manifest.json.
// Field names use lowerCamel for forward-compat with the OCI annotation
// shape Slice 2 will adopt.
type ImageManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Name          string           `json:"name"`
	Tag           string           `json:"tag"`
	OSType        string           `json:"osType,omitempty"`
	SourceVM      string           `json:"sourceVM,omitempty"`
	BaseImage     string           `json:"baseImage,omitempty"`
	DiskSHA256    string           `json:"diskSHA256"`
	DiskSize      int64            `json:"diskSize"`
	CreatedAt     time.Time        `json:"createdAt"`
	SourceConfig  *vmconfig.Config `json:"sourceConfig,omitempty"`
}

// ImageRef is a parsed name[:tag] image reference.
type ImageRef struct {
	Name string
	Tag  string
}

// String renders the canonical "name:tag" form.
func (r ImageRef) String() string { return r.Name + ":" + r.Tag }

// Path returns the on-disk directory for this image ref.
func (r ImageRef) Path() string {
	return filepath.Join(ImagesBaseDir(), r.Name, r.Tag)
}

// ParseImageRef parses "name" or "name:tag" into an ImageRef. Default
// tag is "latest". The name and tag are validated against the same
// allow-set used by snapshot names so they are safe path components.
func ParseImageRef(s string) (ImageRef, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return ImageRef{}, errors.New("image ref must not be empty")
	}
	if strings.Count(s, ":") > 1 {
		return ImageRef{}, fmt.Errorf("image ref %q contains multiple ':'", s)
	}
	name, tag, hasTag := strings.Cut(s, ":")
	if name == "" {
		return ImageRef{}, fmt.Errorf("image ref %q has empty name", s)
	}
	if !hasTag {
		tag = "latest"
	}
	if tag == "" {
		return ImageRef{}, fmt.Errorf("image ref %q has empty tag", s)
	}
	if err := validateImageComponent(name); err != nil {
		return ImageRef{}, fmt.Errorf("image name: %w", err)
	}
	if err := validateImageComponent(tag); err != nil {
		return ImageRef{}, fmt.Errorf("image tag: %w", err)
	}
	return ImageRef{Name: name, Tag: tag}, nil
}

// validateImageComponent is intentionally strict: alnum, '-', '_', '.';
// 1..128 chars; not "." or "..". Keeps refs trivially safe as path
// components and as future OCI repo/tag values.
func validateImageComponent(s string) error {
	if len(s) == 0 || len(s) > 128 {
		return fmt.Errorf("%q must be 1..128 characters", s)
	}
	if s == "." || s == ".." {
		return fmt.Errorf("%q is reserved", s)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return fmt.Errorf("%q contains invalid character %q", s, r)
		}
	}
	return nil
}

// ImageExists reports whether the image at ref has a manifest on disk.
func ImageExists(ref ImageRef) bool {
	_, err := os.Stat(filepath.Join(ref.Path(), "manifest.json"))
	return err == nil
}

// LoadImageManifest reads the manifest at ref or returns an error if
// the image does not exist.
func LoadImageManifest(ref ImageRef) (*ImageManifest, error) {
	data, err := os.ReadFile(filepath.Join(ref.Path(), "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read image manifest: %w", err)
	}
	var m ImageManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse image manifest: %w", err)
	}
	return &m, nil
}

// BuildImageOptions configures cove image build.
type BuildImageOptions struct {
	SourceVM string
	Ref      ImageRef
	Now      func() time.Time
}

// BuildImage snapshots a stopped VM into the local image store. The
// disk is APFS-clonefile'd (CoW) into the image dir; aux/hw.model/
// machine.id are byte-copied. The manifest records sha256(disk),
// source config, and creation time. The source VM must exist and not
// be running (run.lock not held).
func BuildImage(opts BuildImageOptions) (*ImageManifest, error) {
	if strings.TrimSpace(opts.SourceVM) == "" {
		return nil, errors.New("image build: source VM name required")
	}
	if opts.Ref.Name == "" || opts.Ref.Tag == "" {
		return nil, errors.New("image build: image ref required")
	}
	srcDir := vmconfig.Path(opts.SourceVM)
	if !vmconfig.Validate(srcDir) {
		return nil, fmt.Errorf("image build: source VM not found: %s", opts.SourceVM)
	}
	osType := vmconfig.DetectOSType(srcDir)
	if osType == "Linux" {
		// Slice 1 keeps the shape macOS-specific to avoid forked semantics.
		// Linux images can land in Slice 2 alongside the wire format.
		return nil, fmt.Errorf("image build: Linux source VMs are not supported in Slice 1 (OSType=%s)", osType)
	}

	// Refuse to snapshot a running VM. Probe-and-release the run.lock.
	srcLock, err := acquireRunLockHook(srcDir)
	if err != nil {
		if errors.Is(err, ErrRunLockHeld) {
			return nil, fmt.Errorf("image build: source VM %q is running; stop it first", opts.SourceVM)
		}
		return nil, fmt.Errorf("image build: probe source run.lock: %w", err)
	}
	if releaseErr := srcLock.Release(); releaseErr != nil {
		fmt.Fprintf(os.Stderr, "warning: release source run.lock: %v\n", releaseErr)
	}

	imgDir := opts.Ref.Path()
	if _, err := os.Stat(imgDir); err == nil {
		return nil, fmt.Errorf("image build: image %s already exists at %s", opts.Ref, imgDir)
	}
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		return nil, fmt.Errorf("image build: create image dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(imgDir) }

	// Disk: prefer clonefile; fall back to byte-copy if the underlying
	// filesystem refuses (non-APFS image store, separate volume).
	srcDisk := filepath.Join(srcDir, "disk.img")
	dstDisk := filepath.Join(imgDir, "disk.img")
	if err := cloneFile(srcDisk, dstDisk); err != nil {
		if copyErr := copyFile(srcDisk, dstDisk); copyErr != nil {
			cleanup()
			return nil, fmt.Errorf("image build: clone disk: %w (copy fallback: %v)", err, copyErr)
		}
	}

	// Identity files: byte copy.
	for _, f := range []string{"aux.img", "hw.model", "machine.id"} {
		src := filepath.Join(srcDir, f)
		if _, err := os.Stat(src); err != nil {
			if f == "machine.id" {
				continue // optional in source; image cold-boots fine without one
			}
			cleanup()
			return nil, fmt.Errorf("image build: source missing %s: %w", f, err)
		}
		if err := copyFile(src, filepath.Join(imgDir, f)); err != nil {
			cleanup()
			return nil, fmt.Errorf("image build: copy %s: %w", f, err)
		}
	}

	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	hash, size, err := sha256AndSize(dstDisk)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("image build: hash disk: %w", err)
	}
	srcCfg, _ := vmconfig.Load(srcDir)
	manifest := &ImageManifest{
		SchemaVersion: 1,
		Name:          opts.Ref.Name,
		Tag:           opts.Ref.Tag,
		OSType:        osType,
		SourceVM:      opts.SourceVM,
		BaseImage:     "",
		DiskSHA256:    hash,
		DiskSize:      size,
		CreatedAt:     now(),
		SourceConfig:  srcCfg,
	}
	if srcCfg != nil && srcCfg.ParentImage != "" {
		manifest.BaseImage = srcCfg.ParentImage
	}
	if err := writeImageManifest(imgDir, manifest); err != nil {
		cleanup()
		return nil, err
	}
	return manifest, nil
}

func writeImageManifest(dir string, m *ImageManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	path := filepath.Join(dir, "manifest.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename manifest: %w", err)
	}
	return nil
}

func sha256AndSize(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ImageEntry is a row in `cove image list`.
type ImageEntry struct {
	Ref      ImageRef
	Manifest *ImageManifest
}

// ListImages walks the local image store and returns one entry per
// (name, tag) pair that has a readable manifest.json.
func ListImages() ([]ImageEntry, error) {
	root := ImagesBaseDir()
	names, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read images dir: %w", err)
	}
	var out []ImageEntry
	for _, ne := range names {
		if !ne.IsDir() {
			continue
		}
		tags, err := os.ReadDir(filepath.Join(root, ne.Name()))
		if err != nil {
			continue
		}
		for _, te := range tags {
			if !te.IsDir() {
				continue
			}
			ref := ImageRef{Name: ne.Name(), Tag: te.Name()}
			m, err := LoadImageManifest(ref)
			if err != nil {
				continue
			}
			out = append(out, ImageEntry{Ref: ref, Manifest: m})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ref.Name != out[j].Ref.Name {
			return out[i].Ref.Name < out[j].Ref.Name
		}
		return out[i].Ref.Tag < out[j].Ref.Tag
	})
	return out, nil
}

// VMsForkedFromImage returns names of VMs in vmconfig.BaseDir whose
// config.json records ParentImage == ref.String(). This is the gate
// used by `cove image rm` to refuse deletion while live forks remain.
func VMsForkedFromImage(ref ImageRef) ([]string, error) {
	entries, err := os.ReadDir(vmconfig.BaseDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read vm base dir: %w", err)
	}
	var hits []string
	want := ref.String()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(vmconfig.BaseDir(), e.Name())
		cfg, err := vmconfig.Load(dir)
		if err != nil {
			continue
		}
		if cfg.ParentImage == want {
			hits = append(hits, e.Name())
		}
	}
	sort.Strings(hits)
	return hits, nil
}

// DeleteImage removes the image directory after refusing if any VM
// still references it. The caller is responsible for confirming.
func DeleteImage(ref ImageRef) error {
	if !ImageExists(ref) {
		return fmt.Errorf("image %s not found", ref)
	}
	children, err := VMsForkedFromImage(ref)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		return fmt.Errorf("image %s still has %d forked VM(s): %s", ref, len(children), strings.Join(children, ", "))
	}
	if err := os.RemoveAll(ref.Path()); err != nil {
		return fmt.Errorf("remove image: %w", err)
	}
	// Best-effort: remove the parent name dir if it became empty.
	parent := filepath.Dir(ref.Path())
	if entries, err := os.ReadDir(parent); err == nil && len(entries) == 0 {
		os.Remove(parent)
	}
	return nil
}

// MaterializeImageOptions configures fork-from-image.
type MaterializeImageOptions struct {
	Ref       ImageRef
	ChildName string
	Ephemeral bool
	Now       func() time.Time
}

// MaterializeImage creates a fresh VM bundle in vmconfig.BaseDir from
// the image at ref. Disk is APFS-clonefile'd (CoW); aux/hw.model are
// byte-copied; a fresh machine.id is generated. The child's config
// records ParentImage = ref.String() so `cove image rm` can refuse
// while it lives. When Ephemeral is true, an .ephemeral sentinel is
// dropped so the existing fork_ephemeral.go cleanup + GC sweeps the
// child after stop.
func MaterializeImage(opts MaterializeImageOptions) (string, error) {
	if !ImageExists(opts.Ref) {
		return "", fmt.Errorf("materialize: image %s not found", opts.Ref)
	}
	if strings.TrimSpace(opts.ChildName) == "" {
		now := opts.Now
		if now == nil {
			now = func() time.Time { return time.Now().UTC() }
		}
		opts.ChildName = fmt.Sprintf("%s-%s-%s", opts.Ref.Name, opts.Ref.Tag, now().Format("20060102-150405"))
	}
	childDir := vmconfig.Path(opts.ChildName)
	if _, err := os.Stat(childDir); err == nil {
		return "", fmt.Errorf("materialize: vm %q already exists", opts.ChildName)
	}
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		return "", fmt.Errorf("materialize: create child dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(childDir) }

	imgDir := opts.Ref.Path()
	srcDisk := filepath.Join(imgDir, "disk.img")
	dstDisk := filepath.Join(childDir, "disk.img")
	if err := cloneFile(srcDisk, dstDisk); err != nil {
		if copyErr := copyFile(srcDisk, dstDisk); copyErr != nil {
			cleanup()
			return "", fmt.Errorf("materialize: clone disk: %w (copy fallback: %v)", err, copyErr)
		}
	}
	for _, f := range []string{"aux.img", "hw.model"} {
		if err := copyFile(filepath.Join(imgDir, f), filepath.Join(childDir, f)); err != nil {
			cleanup()
			return "", fmt.Errorf("materialize: copy %s: %w", f, err)
		}
	}
	// Generate fresh machine identity for the child. Mirrors CloneVM's
	// CopyMachineID:false path so `cove image rm` and `cove fork` produce
	// indistinguishable per-VM identity.
	if err := generateMachineID(childDir); err != nil {
		cleanup()
		return "", fmt.Errorf("materialize: generate machine ID: %w", err)
	}

	cfg, err := vmconfig.Load(childDir)
	if err != nil {
		cleanup()
		return "", fmt.Errorf("materialize: load child config: %w", err)
	}
	cfg.ParentImage = opts.Ref.String()
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	cfg.ForkedAt = now()
	if err := vmconfig.Save(childDir, cfg); err != nil {
		cleanup()
		return "", fmt.Errorf("materialize: save child config: %w", err)
	}

	if opts.Ephemeral {
		if err := os.WriteFile(filepath.Join(childDir, ephemeralSentinel), nil, 0o644); err != nil {
			cleanup()
			return "", fmt.Errorf("materialize: write ephemeral sentinel: %w", err)
		}
	}
	return childDir, nil
}
