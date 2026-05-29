package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tmc/cove/internal/imagestore"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

const (
	coveImageArtifactType = "application/vnd.cove.vm.image.v1"
	coveImageConfigType   = "application/vnd.cove.vm.config.v1+json"
	coveImageDiskType     = "application/vnd.cove.vm.disk.v1+raw+gzip"
	coveImageFileType     = "application/vnd.cove.vm.file.v1+raw"

	ociTitleAnnotation = "org.opencontainers.image.title"
)

type registryImageRef struct {
	registry.Reference
}

func parseRegistryImageRef(s string) (registryImageRef, error) {
	ref, err := registry.ParseReference(strings.TrimSpace(s))
	if err != nil {
		return registryImageRef{}, fmt.Errorf("registry ref: %w", err)
	}
	if !isRegistryHost(ref.Registry) {
		return registryImageRef{}, fmt.Errorf("registry ref: registry %q must be localhost, a dotted host, or host:port", ref.Registry)
	}
	if ref.Reference == "" {
		return registryImageRef{}, errors.New("registry ref must include a tag or digest")
	}
	return registryImageRef{Reference: ref}, nil
}

func isRegistryReference(s string) bool {
	_, err := parseRegistryImageRef(s)
	return err == nil
}

func isRegistryHost(host string) bool {
	return host == "localhost" || strings.Contains(host, ".") || strings.Contains(host, ":")
}

func newImageRepository(ref registryImageRef) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref.String())
	if err != nil {
		return nil, fmt.Errorf("registry repository: %w", err)
	}
	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{DetectDefaultNativeStore: true})
	if err != nil {
		return nil, fmt.Errorf("registry auth: load docker credentials: %w", err)
	}
	repo.Client = &auth.Client{
		Client:     auth.DefaultClient.Client,
		Header:     auth.DefaultClient.Header,
		Cache:      auth.DefaultClient.Cache,
		Credential: credentials.Credential(store),
	}
	if ref.Registry == "localhost" || strings.HasPrefix(ref.Registry, "localhost:") {
		repo.PlainHTTP = true
	}
	return repo, nil
}

func ensurePrivateRegistryPush(ref registryImageRef) error {
	if os.Getenv("COVE_ALLOW_PUBLIC_PUSH") == "1" {
		return nil
	}
	if !isPublicRegistry(ref.Registry) {
		return nil
	}
	return fmt.Errorf("image push: refusing public registry %s (set COVE_ALLOW_PUBLIC_PUSH=1 to override)", ref.Registry)
}

func isPublicRegistry(registry string) bool {
	switch strings.ToLower(strings.TrimSpace(registry)) {
	case "docker.io", "index.docker.io", "registry-1.docker.io", "ghcr.io", "quay.io", "registry.gitlab.com":
		return true
	default:
		return false
	}
}

func PushImageToRegistry(ctx context.Context, ref imagestore.Ref, dst string) (ocispec.Descriptor, error) {
	remoteRef, err := parseRegistryImageRef(dst)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := ensurePrivateRegistryPush(remoteRef); err != nil {
		return ocispec.Descriptor{}, err
	}
	repo, err := newImageRepository(remoteRef)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return PushImageToTarget(ctx, ref, repo, remoteRef.Reference.ReferenceOrDefault())
}

func PullImageFromRegistry(ctx context.Context, src, overrideTag string, force bool) (imagestore.Ref, ocispec.Descriptor, error) {
	remoteRef, err := parseRegistryImageRef(src)
	if err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, err
	}
	repo, err := newImageRepository(remoteRef)
	if err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, err
	}
	ref, desc, err := PullImageFromTarget(ctx, repo, remoteRef.Reference.ReferenceOrDefault(), overrideTag, force)
	if err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, err
	}
	return ref, desc, nil
}

func PushImageToTarget(ctx context.Context, ref imagestore.Ref, target oras.Target, targetRef string) (ocispec.Descriptor, error) {
	if !imagestore.Exists(ref) {
		return ocispec.Descriptor{}, fmt.Errorf("image push: %s not found in store", ref)
	}
	if targetRef == "" {
		return ocispec.Descriptor{}, errors.New("image push: target ref required")
	}
	imgDir := ref.Path()
	manifest, err := LoadImageManifest(ref)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("image push: %w", err)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("image push: marshal manifest: %w", err)
	}
	configDesc := content.NewDescriptorFromBytes(coveImageConfigType, manifestBytes)
	configDesc.Annotations = map[string]string{ociTitleAnnotation: "manifest.json"}
	if err := pushTargetBlob(ctx, target, configDesc, bytes.NewReader(manifestBytes)); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("image push: push manifest metadata: %w", err)
	}

	diskDesc, cleanup, err := pushCompressedDisk(ctx, target, filepath.Join(imgDir, "disk.img"))
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer cleanup()
	layers := []ocispec.Descriptor{diskDesc}
	for _, name := range imagestore.LayerFiles[1:] {
		desc, err := pushImageFile(ctx, target, filepath.Join(imgDir, name), name, coveImageFileType)
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		layers = append(layers, desc)
	}

	created := manifest.CreatedAt.UTC().Format(time.RFC3339)
	if manifest.CreatedAt.IsZero() {
		created = time.Now().UTC().Format(time.RFC3339)
	}
	root, err := oras.PackManifest(ctx, target, oras.PackManifestVersion1_1, coveImageArtifactType, oras.PackManifestOptions{
		ConfigDescriptor: &configDesc,
		Layers:           layers,
		ManifestAnnotations: map[string]string{
			ocispec.AnnotationCreated: created,
			"dev.cove.image.name":     ref.Name,
			"dev.cove.image.tag":      ref.Tag,
		},
	})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("image push: pack oci manifest: %w", err)
	}
	if err := target.Tag(ctx, root, targetRef); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("image push: tag %s: %w", targetRef, err)
	}
	return root, nil
}

func PullImageFromTarget(ctx context.Context, target oras.ReadOnlyTarget, srcRef, overrideTag string, force bool) (imagestore.Ref, ocispec.Descriptor, error) {
	if srcRef == "" {
		return imagestore.Ref{}, ocispec.Descriptor{}, errors.New("image pull: source ref required")
	}
	root, err := target.Resolve(ctx, srcRef)
	if err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: resolve %s: %w", srcRef, err)
	}
	manifestBytes, err := content.FetchAll(ctx, target, root)
	if err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: fetch oci manifest: %w", err)
	}
	var ociManifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &ociManifest); err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: parse oci manifest: %w", err)
	}
	if ociManifest.ArtifactType != coveImageArtifactType {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: artifact type %q is not %q", ociManifest.ArtifactType, coveImageArtifactType)
	}
	if ociManifest.Config.MediaType != coveImageConfigType {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: config media type %q is not %q", ociManifest.Config.MediaType, coveImageConfigType)
	}
	configBytes, err := content.FetchAll(ctx, target, ociManifest.Config)
	if err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: fetch image metadata: %w", err)
	}
	var m imagestore.Manifest
	if err := json.Unmarshal(configBytes, &m); err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: parse image metadata: %w", err)
	}

	refSpec := m.Name + ":" + m.Tag
	if overrideTag != "" {
		refSpec = overrideTag
	}
	ref, err := ParseImageRef(refSpec)
	if err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: invalid ref %q: %w", refSpec, err)
	}
	if imagestore.Exists(ref) && !force {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: %s already exists (use -force to overwrite)", ref)
	}

	dstDir := ref.Path()
	tmpDir := dstDir + ".tmp"
	if err := os.RemoveAll(tmpDir); err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: clear staging: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: mkdir staging: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(tmpDir)
		}
	}()

	m.Name = ref.Name
	m.Tag = ref.Tag
	manifestOut, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: marshal image metadata: %w", err)
	}
	manifestOut = append(manifestOut, '\n')
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), manifestOut, 0o644); err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: write manifest: %w", err)
	}

	needed := map[string]bool{}
	for _, name := range imagestore.LayerFiles {
		needed[name] = true
	}
	for _, layer := range ociManifest.Layers {
		title := layer.Annotations[ociTitleAnnotation]
		switch {
		case layer.MediaType == coveImageDiskType || title == "disk.img.gz":
			if err := fetchCompressedDiskLayer(ctx, target, layer, filepath.Join(tmpDir, "disk.img")); err != nil {
				return imagestore.Ref{}, ocispec.Descriptor{}, err
			}
			delete(needed, "disk.img")
		case needed[title]:
			if err := fetchLayerFile(ctx, target, layer, filepath.Join(tmpDir, title), title); err != nil {
				return imagestore.Ref{}, ocispec.Descriptor{}, err
			}
			delete(needed, title)
		}
	}
	if len(needed) > 0 {
		missing := make([]string, 0, len(needed))
		for name := range needed {
			missing = append(missing, name)
		}
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: oci artifact missing required files: %s", strings.Join(missing, ", "))
	}

	if force && imagestore.Exists(ref) {
		if err := os.RemoveAll(dstDir); err != nil {
			return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: remove existing: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: mkdir parent: %w", err)
	}
	if err := os.Rename(tmpDir, dstDir); err != nil {
		return imagestore.Ref{}, ocispec.Descriptor{}, fmt.Errorf("image pull: rename: %w", err)
	}
	committed = true
	return ref, root, nil
}

func pushCompressedDisk(ctx context.Context, target oras.Target, path string) (ocispec.Descriptor, func(), error) {
	tmp, err := os.CreateTemp("", "cove-disk-*.img.gz")
	if err != nil {
		return ocispec.Descriptor{}, func() {}, fmt.Errorf("image push: create compressed disk: %w", err)
	}
	cleanup := func() { os.Remove(tmp.Name()) }
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			cleanup()
		}
	}()

	src, err := os.Open(path)
	if err != nil {
		return ocispec.Descriptor{}, cleanup, fmt.Errorf("image push: open disk.img: %w", err)
	}
	gz := gzip.NewWriter(tmp)
	if _, err := io.Copy(gz, src); err != nil {
		src.Close()
		gz.Close()
		return ocispec.Descriptor{}, cleanup, fmt.Errorf("image push: compress disk.img: %w", err)
	}
	if err := src.Close(); err != nil {
		gz.Close()
		return ocispec.Descriptor{}, cleanup, fmt.Errorf("image push: close disk.img: %w", err)
	}
	if err := gz.Close(); err != nil {
		return ocispec.Descriptor{}, cleanup, fmt.Errorf("image push: close compressed disk: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return ocispec.Descriptor{}, cleanup, fmt.Errorf("image push: close compressed disk file: %w", err)
	}

	desc, err := pushImageFile(ctx, target, tmp.Name(), "disk.img.gz", coveImageDiskType)
	if err != nil {
		return ocispec.Descriptor{}, cleanup, err
	}
	ok = true
	return desc, cleanup, nil
}

func pushImageFile(ctx context.Context, target oras.Target, path, title, mediaType string) (ocispec.Descriptor, error) {
	desc, err := fileDescriptor(path, mediaType)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("image push: stat %s: %w", title, err)
	}
	desc.Annotations = map[string]string{ociTitleAnnotation: title}
	f, err := os.Open(path)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("image push: open %s: %w", title, err)
	}
	defer f.Close()
	if err := pushTargetBlob(ctx, target, desc, f); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("image push: push %s: %w", title, err)
	}
	return desc, nil
}

func pushTargetBlob(ctx context.Context, target oras.Target, desc ocispec.Descriptor, r io.Reader) error {
	if err := target.Push(ctx, desc, r); err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return err
	}
	return nil
}

func fetchCompressedDiskLayer(ctx context.Context, target oras.ReadOnlyTarget, layer ocispec.Descriptor, dst string) error {
	rc, err := target.Fetch(ctx, layer)
	if err != nil {
		return fmt.Errorf("image pull: fetch disk.img.gz: %w", err)
	}
	defer rc.Close()
	gz, err := gzip.NewReader(rc)
	if err != nil {
		return fmt.Errorf("image pull: gzip disk.img: %w", err)
	}
	defer gz.Close()
	return writePulledLayer(dst, "disk.img", gz)
}

func fetchLayerFile(ctx context.Context, target oras.ReadOnlyTarget, layer ocispec.Descriptor, dst, title string) error {
	rc, err := target.Fetch(ctx, layer)
	if err != nil {
		return fmt.Errorf("image pull: fetch %s: %w", title, err)
	}
	defer rc.Close()
	return writePulledLayer(dst, title, rc)
}

func writePulledLayer(dst, title string, r io.Reader) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("image pull: open %s: %w", title, err)
	}
	if _, err := io.Copy(out, io.LimitReader(r, imagestore.MaxEntryBytes+1)); err != nil {
		out.Close()
		return fmt.Errorf("image pull: write %s: %w", title, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("image pull: close %s: %w", title, err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		return fmt.Errorf("image pull: stat %s: %w", title, err)
	}
	if st.Size() > imagestore.MaxEntryBytes {
		return fmt.Errorf("image pull: %s exceeds %d byte cap", title, imagestore.MaxEntryBytes)
	}
	return nil
}

func fileDescriptor(path, mediaType string) (ocispec.Descriptor, error) {
	f, err := os.Open(path)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	defer f.Close()
	d := digest.Canonical.Digester()
	size, err := io.Copy(d.Hash(), f)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    d.Digest(),
		Size:      size,
	}, nil
}
