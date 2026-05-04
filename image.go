// image.go — local VM image store (Slice 1 of design 024).
//
// An image is a content-addressed snapshot of a stopped VM bundle that
// can be forked into a fresh VM with `cove run -fork-from <image-ref>`.
// Slice 1 stays purely local: no registry, no push/pull, no signing,
// no network code. The on-disk shape mirrors a tiny OCI-style layout
// (repo/tag) so Slice 2 can later wrap it in real OCI artifacts.
//
// Layout: ~/.vz/images/<name>/<tag>/
// Names may contain slash-separated components, for example
// ~/.vz/images/agentkit/linux-base/latest/.
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
	parts := append([]string{ImagesBaseDir()}, strings.Split(r.Name, "/")...)
	parts = append(parts, r.Tag)
	return filepath.Join(parts...)
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
	if err := validateImageName(name); err != nil {
		return ImageRef{}, fmt.Errorf("image name: %w", err)
	}
	if err := validateImageComponent(tag); err != nil {
		return ImageRef{}, fmt.Errorf("image tag: %w", err)
	}
	return ImageRef{Name: name, Tag: tag}, nil
}

func validateImageName(s string) error {
	if strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") || strings.Contains(s, "//") {
		return fmt.Errorf("%q must be slash-separated path components", s)
	}
	for _, part := range strings.Split(s, "/") {
		if err := validateImageComponent(part); err != nil {
			return err
		}
	}
	return nil
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

	if err := copyImageFiles(srcDir, imgDir, osType); err != nil {
		cleanup()
		return nil, err
	}

	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	hash, size, err := sha256AndSize(vmPrimaryDiskPath(imgDir))
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

func copyImageFiles(srcDir, imgDir, osType string) error {
	for _, f := range cloneRequiredFiles(osType) {
		src := filepath.Join(srcDir, f.name)
		if _, err := os.Stat(src); err != nil {
			if !f.required {
				continue
			}
			return fmt.Errorf("image build: source missing %s: %w", f.name, err)
		}
		dst := filepath.Join(imgDir, f.name)
		if f.useClone {
			if err := cloneFile(src, dst); err != nil {
				if copyErr := copyFile(src, dst); copyErr != nil {
					return fmt.Errorf("image build: clone %s: %w (copy fallback: %v)", f.name, err, copyErr)
				}
			}
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("image build: copy %s: %w", f.name, err)
		}
	}
	for _, name := range cloneOptionalFiles(osType) {
		src := filepath.Join(srcDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyFile(src, filepath.Join(imgDir, name)); err != nil {
			return fmt.Errorf("image build: copy %s: %w", name, err)
		}
	}
	return nil
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
	_, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat images dir: %w", err)
	}
	var out []ImageEntry
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path == root {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "manifest.json")); err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 2 {
			return nil
		}
		ref := ImageRef{Name: strings.Join(parts[:len(parts)-1], "/"), Tag: parts[len(parts)-1]}
		m, err := LoadImageManifest(ref)
		if err != nil {
			return nil
		}
		out = append(out, ImageEntry{Ref: ref, Manifest: m})
		return filepath.SkipDir
	})
	if err != nil {
		return nil, fmt.Errorf("walk images dir: %w", err)
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
	// Best-effort: remove empty parent name dirs.
	parent := filepath.Dir(ref.Path())
	for parent != ImagesBaseDir() && strings.HasPrefix(parent, ImagesBaseDir()) {
		entries, err := os.ReadDir(parent)
		if err != nil || len(entries) != 0 {
			break
		}
		os.Remove(parent)
		parent = filepath.Dir(parent)
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

	manifest, err := LoadImageManifest(opts.Ref)
	if err != nil {
		cleanup()
		return "", err
	}
	if err := materializeImageFiles(opts.Ref.Path(), childDir, manifest.OSType); err != nil {
		cleanup()
		return "", err
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

func materializeImageFiles(imgDir, childDir, osType string) error {
	for _, f := range cloneRequiredFiles(osType) {
		if f.name == "machine.id" || f.name == "linux-machine.id" {
			continue
		}
		src := filepath.Join(imgDir, f.name)
		if _, err := os.Stat(src); err != nil {
			if !f.required {
				continue
			}
			return fmt.Errorf("materialize: source missing %s: %w", f.name, err)
		}
		dst := filepath.Join(childDir, f.name)
		if f.useClone {
			if err := cloneFile(src, dst); err != nil {
				if copyErr := copyFile(src, dst); copyErr != nil {
					return fmt.Errorf("materialize: clone %s: %w (copy fallback: %v)", f.name, err, copyErr)
				}
			}
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("materialize: copy %s: %w", f.name, err)
		}
	}
	for _, name := range cloneOptionalFiles(osType) {
		src := filepath.Join(imgDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyFile(src, filepath.Join(childDir, name)); err != nil {
			return fmt.Errorf("materialize: copy %s: %w", name, err)
		}
	}
	switch osType {
	case "Linux":
		// Linux creates linux-machine.id on first boot when absent.
	default:
		if err := generateMachineID(childDir); err != nil {
			return fmt.Errorf("materialize: generate machine ID: %w", err)
		}
	}
	return nil
}
