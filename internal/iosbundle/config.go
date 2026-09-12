package iosbundle

import (
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"path"
	"strings"
	"unicode"
)

// Config is the ios object in a cove config.json file.
// The zero value is invalid. Use DefaultConfig for a blank research guest.
type Config struct {
	SchemaVersion  int     `json:"schemaVersion"`
	Profile        string  `json:"profile"`
	Variant        string  `json:"variant"`
	FirmwareDigest string  `json:"firmwareDigest,omitempty"`
	Display        Display `json:"display"`
	Network        string  `json:"network"`
	MAC            string  `json:"mac,omitempty"`
	BootArgs       string  `json:"bootArgs,omitempty"`
	ROM            string  `json:"rom,omitempty"`
	SEPROM         string  `json:"sepROM,omitempty"`
	NoBinpack      bool    `json:"noBinpack,omitempty"`
	NoVphoned      bool    `json:"noVphoned,omitempty"`
}

// Display records guest pixel geometry, independently of host window size.
type Display struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	PPI    int     `json:"ppi"`
	Scale  float64 `json:"scale"`
}

// DefaultConfig returns the pinned research hardware profile, without firmware.
func DefaultConfig() Config {
	return Config{SchemaVersion: 1, Profile: "vresearch101", Variant: "regular",
		Display: Display{Width: 1290, Height: 2796, PPI: 460, Scale: 3}, Network: "nat"}
}

// Validate checks persistent values without probing host support or firmware.
func (c Config) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("unsupported ios schema version %d", c.SchemaVersion)
	}
	if c.Profile != "vresearch101" {
		return fmt.Errorf("unknown ios profile %q", c.Profile)
	}
	switch c.Variant {
	case "less", "regular", "dev", "jb", "exp":
	default:
		return fmt.Errorf("unknown ios variant %q", c.Variant)
	}
	if c.NoBinpack && c.Variant != "less" {
		return fmt.Errorf("no-binpack requires the less variant")
	}
	// no-vphoned also controls runtime staging, independently of patch variants.
	if c.Display.Width <= 0 || c.Display.Height <= 0 || c.Display.PPI <= 0 || c.Display.Scale <= 0 || math.IsNaN(c.Display.Scale) || math.IsInf(c.Display.Scale, 0) {
		return fmt.Errorf("ios display dimensions, ppi and scale must be positive")
	}
	switch c.Network {
	case "nat", "none":
	default:
		if iface, ok := strings.CutPrefix(c.Network, "bridged:"); !ok || iface == "" || strings.ContainsAny(iface, "/:") || strings.ContainsFunc(iface, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return fmt.Errorf("invalid ios network %q", c.Network)
		}
	}
	if c.MAC != "" {
		a, err := net.ParseMAC(c.MAC)
		if err != nil || len(a) != 6 || a[0]&1 != 0 {
			return fmt.Errorf("invalid ios unicast mac %q", c.MAC)
		}
	}
	for _, file := range []struct{ name, path string }{{"rom", c.ROM}, {"sepROM", c.SEPROM}} {
		if file.path != "" && (path.IsAbs(file.path) || path.Clean(file.path) != file.path || file.path == "." || file.path == ".." || strings.HasPrefix(file.path, "../") || strings.ContainsAny(file.path, "\\:\x00")) {
			return fmt.Errorf("ios %s must be a clean bundle-relative path", file.name)
		}
	}
	if c.FirmwareDigest != "" {
		b, err := hex.DecodeString(c.FirmwareDigest)
		if err != nil || len(b) != 32 {
			return fmt.Errorf("ios firmware digest must be a sha256 hex string")
		}
	}
	return nil
}
