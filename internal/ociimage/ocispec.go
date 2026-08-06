package ociimage

import "github.com/tmc/cove-ocispec"

// The OCI image-spec types and reference syntax live in cove-ocispec so that
// marina's control plane can depend on them without depending on cove. They are
// re-exported here so callers inside cove keep using ociimage unchanged.

// Media types defined by the OCI image spec and Docker manifest schema 2.
const (
	MediaTypeImageManifest  = ocispec.MediaTypeImageManifest
	MediaTypeImageIndex     = ocispec.MediaTypeImageIndex
	MediaTypeImageConfig    = ocispec.MediaTypeImageConfig
	MediaTypeDockerManifest = ocispec.MediaTypeDockerManifest
	MediaTypeDockerList     = ocispec.MediaTypeDockerList
	MediaTypeLayer          = ocispec.MediaTypeLayer
)

// Image-spec types.
type (
	// Descriptor is an OCI content descriptor.
	Descriptor = ocispec.Descriptor
	// Manifest is an OCI image manifest.
	Manifest = ocispec.Manifest
	// Index is an OCI image index or Docker manifest list.
	Index = ocispec.Index
	// IndexDescriptor is an image-index descriptor with optional platform metadata.
	IndexDescriptor = ocispec.IndexDescriptor
	// Platform describes the target platform for an image-index descriptor.
	Platform = ocispec.Platform
	// Reference is a registry reference split into its parts.
	Reference = ocispec.Reference
)

// ParsePlatform parses an OCI platform in os/arch or os/arch/variant form.
func ParsePlatform(value string) (Platform, error) { return ocispec.ParsePlatform(value) }

// ParseReference parses refs such as ghcr.io/acme/macos:latest.
func ParseReference(ref string) (Reference, error) { return ocispec.ParseReference(ref) }

// ValidateTag reports whether tag is a valid OCI tag.
func ValidateTag(tag string) error { return ocispec.ValidateTag(tag) }
