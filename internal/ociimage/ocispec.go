package ociimage

import (
	"fmt"
	"strings"
)

const (
	MediaTypeImageManifest  = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeImageIndex     = "application/vnd.oci.image.index.v1+json"
	MediaTypeImageConfig    = "application/vnd.oci.image.config.v1+json"
	MediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeLayer          = "application/octet-stream"
)

// Descriptor is an OCI content descriptor.
type Descriptor struct {
	MediaType   string            `json:"mediaType"`
	Size        int64             `json:"size"`
	Digest      string            `json:"digest"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Manifest is the OCI image manifest shape cove writes.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Index is an OCI image index or Docker manifest list.
type Index struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Manifests     []IndexDescriptor `json:"manifests"`
}

// IndexDescriptor is an image-index descriptor with optional platform metadata.
type IndexDescriptor struct {
	Descriptor
	Platform *Platform `json:"platform,omitempty"`
}

// Platform describes the target platform for an image-index descriptor.
type Platform struct {
	Architecture string   `json:"architecture,omitempty"`
	OS           string   `json:"os,omitempty"`
	OSVersion    string   `json:"os.version,omitempty"`
	Variant      string   `json:"variant,omitempty"`
	Features     []string `json:"features,omitempty"`
}

// ParsePlatform parses an OCI platform in os/arch or os/arch/variant form.
func ParsePlatform(value string) (Platform, error) {
	var out Platform
	value = strings.TrimSpace(value)
	if value == "" {
		return out, fmt.Errorf("platform is empty")
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return out, fmt.Errorf("platform %q must be os/arch or os/arch/variant", value)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return out, fmt.Errorf("platform %q has an empty component", value)
		}
	}
	out.OS = strings.ToLower(parts[0])
	out.Architecture = strings.ToLower(parts[1])
	if len(parts) == 3 {
		out.Variant = strings.ToLower(parts[2])
	}
	return out, nil
}
