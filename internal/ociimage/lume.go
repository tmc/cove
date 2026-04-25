package ociimage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// LumeTarLayerMediaTypePrefix identifies tar-split layers in lume's ghcr.io
// images. The full mediaType carries parameters: part.number=<n>;part.total=<m>.
const LumeTarLayerMediaTypePrefix = "application/vnd.oci.image.layer.v1.tar"

// Lume sidecar file names. These are layer titles, not mediatypes — lume
// addresses non-disk content by org.opencontainers.image.title.
const (
	LumeNvramTitle      = "nvram.bin"
	LumeConfigTitle     = "config.json"
	LumeDiskPartPrefix  = "disk.img.part."
)

// LumeLayer pairs a sorted disk-part descriptor with its decoded part number.
type LumeLayer struct {
	Descriptor Descriptor
	// PartNumber is 1-based, taken from the mediaType parameter when present
	// and otherwise inferred from the title suffix (aa=1, ab=2, ...).
	PartNumber int
	// Title is the value of org.opencontainers.image.title — used for sort
	// order when part.number is missing or duplicated.
	Title string
}

// LumeManifest is the normalized view of a lume tar-split manifest.
type LumeManifest struct {
	// DiskParts are tar-split parts that concatenate (in order) to disk.img.
	DiskParts []LumeLayer
	// NvramLayer is the layer titled "nvram.bin", if present.
	NvramLayer *Descriptor
	// ConfigLayer is the layer titled "config.json", if present.
	ConfigLayer *Descriptor
}

// IsLumeManifest reports whether m looks like a lume tar-split manifest:
// no Cove annotations, at least one layer carrying the lume tar mediaType
// (with part.number parameter) or a disk.img.part.* title.
func IsLumeManifest(m Manifest) bool {
	for _, key := range []string{
		CoveUncompressedDiskSize,
		CoveHWModelDigest,
		CoveAuxDigest,
		CoveUploadTime,
	} {
		if _, ok := m.Annotations[key]; ok {
			return false
		}
		if lumeKey, ok := coveToLume[key]; ok {
			if _, ok := m.Annotations[lumeKey]; ok {
				return false
			}
		}
	}
	for _, layer := range m.Layers {
		if isLumeTarLayer(layer) {
			return true
		}
	}
	return false
}

func isLumeTarLayer(d Descriptor) bool {
	if strings.HasPrefix(d.MediaType, LumeTarLayerMediaTypePrefix) {
		return true
	}
	if title := d.Annotations["org.opencontainers.image.title"]; strings.HasPrefix(title, LumeDiskPartPrefix) {
		return true
	}
	return false
}

// ParseLumeManifest extracts the tar-split disk parts and lume sidecars.
// Returns an error if the manifest claims to be lume but lacks any disk
// parts or has unparseable part numbers.
func ParseLumeManifest(m Manifest) (LumeManifest, error) {
	var out LumeManifest
	var disks []LumeLayer
	for _, layer := range m.Layers {
		title := layer.Annotations["org.opencontainers.image.title"]
		switch {
		case title == LumeNvramTitle:
			d := layer
			out.NvramLayer = &d
		case title == LumeConfigTitle:
			d := layer
			out.ConfigLayer = &d
		case strings.HasPrefix(title, LumeDiskPartPrefix) || strings.HasPrefix(layer.MediaType, LumeTarLayerMediaTypePrefix):
			part, err := lumePartNumber(layer)
			if err != nil {
				return out, fmt.Errorf("parse lume manifest: %w", err)
			}
			disks = append(disks, LumeLayer{
				Descriptor: layer,
				PartNumber: part,
				Title:      title,
			})
		}
	}
	if len(disks) == 0 {
		return out, fmt.Errorf("parse lume manifest: no disk parts found")
	}
	sort.SliceStable(disks, func(i, j int) bool {
		if disks[i].PartNumber != disks[j].PartNumber {
			return disks[i].PartNumber < disks[j].PartNumber
		}
		return disks[i].Title < disks[j].Title
	})
	for i, d := range disks {
		if d.PartNumber == 0 {
			disks[i].PartNumber = i + 1
		}
	}
	out.DiskParts = disks
	return out, nil
}

// lumePartNumber decodes the part number from a layer's mediaType
// parameters (part.number=N), falling back to the title suffix (aa=1).
func lumePartNumber(d Descriptor) (int, error) {
	if n, ok := mediaTypeParam(d.MediaType, "part.number"); ok {
		v, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("invalid part.number %q: %w", n, err)
		}
		return v, nil
	}
	title := d.Annotations["org.opencontainers.image.title"]
	suffix := strings.TrimPrefix(title, LumeDiskPartPrefix)
	if suffix == title || suffix == "" {
		return 0, nil
	}
	// Two-letter base-26 sequence: aa=1, ab=2, ..., az=26, ba=27, ...
	if len(suffix) != 2 {
		return 0, nil
	}
	a := int(suffix[0])
	b := int(suffix[1])
	if a < 'a' || a > 'z' || b < 'a' || b > 'z' {
		return 0, nil
	}
	return (a-'a')*26 + (b - 'a') + 1, nil
}

// mediaTypeParam returns the value of a parameter from an OCI mediaType
// such as "application/vnd.oci.image.layer.v1.tar;part.number=3".
func mediaTypeParam(mediaType, name string) (string, bool) {
	parts := strings.Split(mediaType, ";")
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			continue
		}
		if strings.TrimSpace(p[:eq]) == name {
			return strings.TrimSpace(p[eq+1:]), true
		}
	}
	return "", false
}

// LumeConfig is the subset of lume's config.json that maps onto cove fields.
// Lume's full schema carries more (display, audio, etc.); we only decode the
// fields that influence VM boot. Unknown fields are preserved by callers via
// the raw bytes if they want to round-trip the file.
type LumeConfig struct {
	OS              string `json:"os,omitempty"`
	CPU             int    `json:"cpu,omitempty"`
	Memory          string `json:"memory,omitempty"`
	DiskSize        string `json:"diskSize,omitempty"`
	MachineIdentifier string `json:"machineIdentifier,omitempty"`
	HardwareModel   string `json:"hardwareModel,omitempty"`
	MACAddress      string `json:"macAddress,omitempty"`
}

// DecodeLumeConfig parses the bytes of config.json into LumeConfig. Tolerates
// unknown fields and capitalisation variants (lume has shipped both camelCase
// and PascalCase historically — accept either by lowercasing keys).
func DecodeLumeConfig(data []byte) (LumeConfig, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return LumeConfig{}, fmt.Errorf("decode lume config: %w", err)
	}
	lower := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		lower[strings.ToLower(k)] = v
	}
	var out LumeConfig
	for _, kv := range []struct {
		key string
		dst any
	}{
		{"os", &out.OS},
		{"cpu", &out.CPU},
		{"memory", &out.Memory},
		{"disksize", &out.DiskSize},
		{"machineidentifier", &out.MachineIdentifier},
		{"hardwaremodel", &out.HardwareModel},
		{"macaddress", &out.MACAddress},
	} {
		v, ok := lower[kv.key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(v, kv.dst); err != nil {
			return out, fmt.Errorf("decode lume config %s: %w", kv.key, err)
		}
	}
	return out, nil
}
