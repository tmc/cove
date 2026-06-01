package fleetcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/cove/internal/ociimage"
)

type imageManifestBundleIdentity struct {
	Path      string
	Ref       string
	Digest    string
	DigestRef string
	Platform  string
}

type imageManifestBundleSummary struct {
	SchemaVersion      int                               `json:"schema_version"`
	Ref                string                            `json:"ref,omitempty"`
	IndexPath          string                            `json:"index_path,omitempty"`
	IndexDigest        string                            `json:"index_digest,omitempty"`
	IndexFileDigest    string                            `json:"index_file_digest,omitempty"`
	SelectedPath       string                            `json:"selected_path,omitempty"`
	ManifestDigest     string                            `json:"manifest_digest,omitempty"`
	SelectedFileDigest string                            `json:"selected_file_digest,omitempty"`
	DigestRef          string                            `json:"digest_ref,omitempty"`
	SelectedDigest     string                            `json:"selected_digest,omitempty"`
	SelectedPlatform   string                            `json:"selected_platform,omitempty"`
	ChildCount         int                               `json:"child_count"`
	Children           []imageManifestBundleChildSummary `json:"children,omitempty"`
}

type imageManifestBundleChildSummary struct {
	Digest     string `json:"digest"`
	Path       string `json:"path,omitempty"`
	FileDigest string `json:"file_digest,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Platform   string `json:"platform,omitempty"`
	Selected   bool   `json:"selected,omitempty"`
}

func resolveImageManifestBundle(path, platform string) (imageManifestBundleIdentity, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return imageManifestBundleIdentity{}, fmt.Errorf("manifest bundle path required")
	}
	summary, err := readImageManifestBundleSummary(path)
	if err != nil {
		return imageManifestBundleIdentity{}, err
	}
	if summary.SchemaVersion != 1 {
		return imageManifestBundleIdentity{}, fmt.Errorf("manifest bundle summary schema_version %d, want 1", summary.SchemaVersion)
	}
	if summary.ChildCount != len(summary.Children) {
		return imageManifestBundleIdentity{}, fmt.Errorf("manifest bundle child_count %d, entries %d", summary.ChildCount, len(summary.Children))
	}
	indexData, index, err := readImageManifestBundleIndex(path, summary)
	if err != nil {
		return imageManifestBundleIdentity{}, err
	}
	if err := verifyImageManifestBundleDigestClaims("index", indexData, summary.IndexFileDigest, summary.IndexDigest); err != nil {
		return imageManifestBundleIdentity{}, err
	}
	selectedData, err := readImageManifestBundleFile(path, imageManifestBundleRelPath(summary.SelectedPath, "selected.json"))
	if err != nil {
		return imageManifestBundleIdentity{}, fmt.Errorf("manifest bundle selected: %w", err)
	}
	if err := verifyImageManifestBundleDigestClaims("selected", selectedData, summary.SelectedFileDigest, summary.ManifestDigest, summary.SelectedDigest); err != nil {
		return imageManifestBundleIdentity{}, err
	}
	children, err := verifyImageManifestBundleChildren(path, summary, index, selectedData)
	if err != nil {
		return imageManifestBundleIdentity{}, err
	}
	selected, err := selectImageManifestBundleChild(summary, children, platform)
	if err != nil {
		return imageManifestBundleIdentity{}, err
	}
	digest := strings.TrimSpace(selected.Digest)
	if digest == "" {
		return imageManifestBundleIdentity{}, fmt.Errorf("manifest bundle selected child missing digest")
	}
	if !validImageManifestBundleDigest(digest) {
		return imageManifestBundleIdentity{}, fmt.Errorf("manifest bundle selected child digest %q is not canonical sha256", digest)
	}
	identity := imageManifestBundleIdentity{
		Path:     path,
		Ref:      strings.TrimSpace(summary.Ref),
		Digest:   digest,
		Platform: strings.TrimSpace(selected.Platform),
	}
	if identity.Platform == "" && strings.TrimSpace(platform) == "" {
		identity.Platform = strings.TrimSpace(summary.SelectedPlatform)
	}
	identity.DigestRef = imageManifestBundleDigestRef(summary, digest)
	return identity, nil
}

func readImageManifestBundleSummary(dir string) (imageManifestBundleSummary, error) {
	var summary imageManifestBundleSummary
	data, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		return summary, fmt.Errorf("manifest bundle read summary.json: %w", err)
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		return summary, fmt.Errorf("manifest bundle parse summary.json: %w", err)
	}
	return summary, nil
}

func readImageManifestBundleIndex(dir string, summary imageManifestBundleSummary) ([]byte, ociimage.Index, error) {
	data, err := readImageManifestBundleFile(dir, imageManifestBundleRelPath(summary.IndexPath, "index.json"))
	if err != nil {
		return nil, ociimage.Index{}, fmt.Errorf("manifest bundle index: %w", err)
	}
	var index ociimage.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, index, fmt.Errorf("manifest bundle parse index.json: %w", err)
	}
	if index.SchemaVersion != 2 {
		return nil, index, fmt.Errorf("manifest bundle index schemaVersion %d, want 2", index.SchemaVersion)
	}
	if index.MediaType != ociimage.MediaTypeImageIndex && index.MediaType != ociimage.MediaTypeDockerList {
		return nil, index, fmt.Errorf("manifest bundle index mediaType %q is not an OCI index or Docker manifest list", index.MediaType)
	}
	return data, index, nil
}

func verifyImageManifestBundleChildren(dir string, summary imageManifestBundleSummary, index ociimage.Index, selectedData []byte) (map[string]imageManifestBundleChildSummary, error) {
	if len(index.Manifests) != len(summary.Children) {
		return nil, fmt.Errorf("manifest bundle index manifests %d, summary children %d", len(index.Manifests), len(summary.Children))
	}
	descriptors := make(map[string]ociimage.IndexDescriptor, len(index.Manifests))
	for _, desc := range index.Manifests {
		if strings.TrimSpace(desc.Digest) == "" {
			return nil, fmt.Errorf("manifest bundle index child missing digest")
		}
		if _, exists := descriptors[desc.Digest]; exists {
			return nil, fmt.Errorf("manifest bundle duplicate index child %s", desc.Digest)
		}
		descriptors[desc.Digest] = desc
	}
	children := make(map[string]imageManifestBundleChildSummary, len(summary.Children))
	var selectedMatches int
	for _, child := range summary.Children {
		digest := strings.TrimSpace(child.Digest)
		if digest == "" {
			return nil, fmt.Errorf("manifest bundle child missing digest")
		}
		if !validImageManifestBundleDigest(digest) {
			return nil, fmt.Errorf("manifest bundle child digest %q is not canonical sha256", digest)
		}
		if _, exists := children[digest]; exists {
			return nil, fmt.Errorf("manifest bundle duplicate child %s", digest)
		}
		desc, ok := descriptors[digest]
		if !ok {
			return nil, fmt.Errorf("manifest bundle summary child %s not present in index", digest)
		}
		if err := verifyImageManifestBundleChild(dir, child, desc); err != nil {
			return nil, err
		}
		if child.Selected {
			selectedMatches++
		}
		if digest == strings.TrimSpace(summary.SelectedDigest) {
			childData, err := readImageManifestBundleFile(dir, imageManifestBundleChildPath(digest))
			if err != nil {
				return nil, fmt.Errorf("manifest bundle selected child %s: %w", digest, err)
			}
			if imageManifestBundleDigestData(childData) != imageManifestBundleDigestData(selectedData) {
				return nil, fmt.Errorf("manifest bundle selected.json differs from %s", imageManifestBundleChildPath(digest))
			}
		}
		children[digest] = child
	}
	for digest := range descriptors {
		if _, ok := children[digest]; !ok {
			return nil, fmt.Errorf("manifest bundle index child %s missing from summary", digest)
		}
	}
	if strings.TrimSpace(summary.SelectedDigest) == "" {
		return nil, fmt.Errorf("manifest bundle summary missing selected_digest")
	}
	if selectedMatches != 1 {
		return nil, fmt.Errorf("manifest bundle selected children %d, want 1", selectedMatches)
	}
	return children, nil
}

func verifyImageManifestBundleChild(dir string, child imageManifestBundleChildSummary, desc ociimage.IndexDescriptor) error {
	digest := strings.TrimSpace(child.Digest)
	path := strings.TrimSpace(child.Path)
	if path == "" {
		path = imageManifestBundleChildPath(digest)
	}
	if path != imageManifestBundleChildPath(digest) {
		return fmt.Errorf("manifest bundle child %s path %q, want %q", digest, path, imageManifestBundleChildPath(digest))
	}
	data, err := readImageManifestBundleFile(dir, path)
	if err != nil {
		return fmt.Errorf("manifest bundle child %s: %w", digest, err)
	}
	if err := verifyImageManifestBundleDigestClaims("child "+digest, data, child.FileDigest, digest, desc.Digest); err != nil {
		return err
	}
	if desc.Size != int64(len(data)) {
		return fmt.Errorf("manifest bundle child %s size %d, file bytes %d", digest, desc.Size, len(data))
	}
	if child.Size != 0 && child.Size != int64(len(data)) {
		return fmt.Errorf("manifest bundle child %s summary size %d, file bytes %d", digest, child.Size, len(data))
	}
	if desc.MediaType != "" && child.MediaType != "" && desc.MediaType != child.MediaType {
		return fmt.Errorf("manifest bundle child %s mediaType %q, summary %q", digest, desc.MediaType, child.MediaType)
	}
	if platform := imageManifestBundlePlatformString(desc.Platform); platform != child.Platform {
		return fmt.Errorf("manifest bundle child %s platform %q, summary %q", digest, platform, child.Platform)
	}
	return nil
}

func selectImageManifestBundleChild(summary imageManifestBundleSummary, children map[string]imageManifestBundleChildSummary, platform string) (imageManifestBundleChildSummary, error) {
	platform = strings.TrimSpace(platform)
	if platform != "" {
		parsed, err := ociimage.ParsePlatform(platform)
		if err != nil {
			return imageManifestBundleChildSummary{}, fmt.Errorf("manifest bundle platform: %w", err)
		}
		want := imageManifestBundlePlatformString(&parsed)
		for _, child := range children {
			if child.Platform == want {
				return child, nil
			}
		}
		return imageManifestBundleChildSummary{}, fmt.Errorf("manifest bundle platform %q not found", want)
	}
	selectedDigest := strings.TrimSpace(summary.SelectedDigest)
	for _, child := range children {
		if child.Digest == selectedDigest || child.Selected {
			return child, nil
		}
	}
	return imageManifestBundleChildSummary{}, fmt.Errorf("manifest bundle selected child %q not found", selectedDigest)
}

func readImageManifestBundleFile(dir, rel string) ([]byte, error) {
	clean, err := imageManifestBundleCleanRelPath(rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, filepath.FromSlash(clean)))
}

func imageManifestBundleRelPath(path, def string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return def
	}
	return path
}

func imageManifestBundleCleanRelPath(path string) (string, error) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("absolute path %q", path)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe path %q", path)
	}
	return clean, nil
}

func verifyImageManifestBundleDigestClaims(label string, data []byte, claims ...string) error {
	got := imageManifestBundleDigestData(data)
	var sawClaim bool
	for _, claim := range claims {
		claim = strings.TrimSpace(claim)
		if claim == "" {
			continue
		}
		sawClaim = true
		if !validImageManifestBundleDigest(claim) {
			return fmt.Errorf("manifest bundle %s digest %q is not canonical sha256", label, claim)
		}
		if claim != got {
			return fmt.Errorf("manifest bundle %s digest %s, file digest %s", label, claim, got)
		}
	}
	if !sawClaim {
		return fmt.Errorf("manifest bundle %s missing digest claim", label)
	}
	return nil
}

func imageManifestBundleDigestData(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validImageManifestBundleDigest(digest string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) || len(digest) != len(prefix)+64 {
		return false
	}
	for _, r := range digest[len(prefix):] {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func imageManifestBundleChildPath(digest string) string {
	name := strings.TrimSpace(digest)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "/", "-")
	return filepath.ToSlash(filepath.Join("manifests", name+".json"))
}

func imageManifestBundlePlatformString(platform *ociimage.Platform) string {
	if platform == nil {
		return ""
	}
	var b strings.Builder
	if platform.OS != "" || platform.Architecture != "" {
		b.WriteString(platform.OS)
		if platform.OS != "" && platform.Architecture != "" {
			b.WriteByte('/')
		}
		b.WriteString(platform.Architecture)
	}
	if platform.Variant != "" {
		if b.Len() > 0 {
			b.WriteByte('/')
		}
		b.WriteString(platform.Variant)
	}
	return b.String()
}

func imageManifestBundleDigestRef(summary imageManifestBundleSummary, digest string) string {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return ""
	}
	if strings.TrimSpace(summary.DigestRef) != "" && strings.HasSuffix(strings.TrimSpace(summary.DigestRef), "@"+digest) {
		return strings.TrimSpace(summary.DigestRef)
	}
	ref, err := ociimage.ParseReference(summary.Ref)
	if err != nil {
		return ""
	}
	return ociimage.Reference{Registry: ref.Registry, Repository: ref.Repository, Digest: digest}.String()
}

func applyImageManifestBundle(bundle, platform string, digest, digestRef, imagePlatform *string, sourceRef *string) error {
	if strings.TrimSpace(bundle) == "" {
		return nil
	}
	identity, err := resolveImageManifestBundle(bundle, platform)
	if err != nil {
		return err
	}
	if err := setOrMatchImageBundleField("image_manifest_digest", digest, identity.Digest); err != nil {
		return err
	}
	if identity.DigestRef != "" {
		if err := setOrMatchImageBundleField("image_digest_ref", digestRef, identity.DigestRef); err != nil {
			return err
		}
	}
	if imagePlatform != nil {
		*imagePlatform = identity.Platform
	}
	if sourceRef != nil {
		if err := validateImageBundleSourceRef(*sourceRef, identity.Ref); err != nil {
			return err
		}
		switch {
		case identity.DigestRef != "":
			*sourceRef = identity.DigestRef
		case strings.TrimSpace(*sourceRef) == "":
			*sourceRef = identity.Ref
		}
	}
	return nil
}

func setOrMatchImageBundleField(name string, dst *string, value string) error {
	if dst == nil || strings.TrimSpace(value) == "" {
		return nil
	}
	cur := strings.TrimSpace(*dst)
	if cur != "" && cur != value {
		return fmt.Errorf("manifest bundle %s %s conflicts with request %s %s", name, value, name, cur)
	}
	*dst = value
	return nil
}

func validateImageBundleSourceRef(sourceRef, bundleRef string) error {
	sourceRef = strings.TrimSpace(sourceRef)
	bundleRef = strings.TrimSpace(bundleRef)
	if sourceRef == "" || bundleRef == "" {
		return nil
	}
	source, err := ociimage.ParseReference(sourceRef)
	if err != nil {
		return fmt.Errorf("manifest bundle source_ref %q is not a registry reference: %w", sourceRef, err)
	}
	bundle, err := ociimage.ParseReference(bundleRef)
	if err != nil {
		return nil
	}
	if source.Registry != bundle.Registry || source.Repository != bundle.Repository {
		return fmt.Errorf("manifest bundle source_ref %s does not match bundle source %s", sourceRef, bundleRef)
	}
	return nil
}

func resolveImagePrepareManifestBundle(req *ImagePrepareRequest) error {
	if err := applyImageManifestBundle(req.ManifestBundle, req.ImagePlatform, &req.ImageManifestDigest, &req.ImageDigestRef, &req.ImagePlatform, &req.SourceRef); err != nil {
		return err
	}
	req.ManifestBundle = ""
	return nil
}

func resolveAssignmentManifestBundle(assignment *Assignment) error {
	if err := applyImageManifestBundle(assignment.ManifestBundle, assignment.ImagePlatform, &assignment.ImageManifestDigest, &assignment.ImageDigestRef, &assignment.ImagePlatform, nil); err != nil {
		return err
	}
	assignment.ManifestBundle = ""
	return nil
}

func resolveWarmPoolManifestBundle(req *WarmPoolRequest) error {
	if err := applyImageManifestBundle(req.ManifestBundle, req.ImagePlatform, &req.ImageManifestDigest, &req.ImageDigestRef, &req.ImagePlatform, nil); err != nil {
		return err
	}
	req.ManifestBundle = ""
	return nil
}

func resolveSandboxManifestBundle(req *SandboxRequest) error {
	if err := applyImageManifestBundle(req.ManifestBundle, req.ImagePlatform, &req.ImageManifestDigest, &req.ImageDigestRef, &req.ImagePlatform, nil); err != nil {
		return err
	}
	req.ManifestBundle = ""
	return nil
}
