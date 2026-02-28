// display.go - Multi-display support for VMs
package main

import (
	"fmt"
	"strconv"
	"strings"

	vz "github.com/tmc/apple/virtualization"
)

// DisplayConfig represents a single display configuration
type DisplayConfig struct {
	Width  int // Width in pixels
	Height int // Height in pixels
	PPI    int // Pixels per inch (optional, default 144)
}

// DefaultDisplayConfig returns the default display configuration
func DefaultDisplayConfig() DisplayConfig {
	return DisplayConfig{
		Width:  1920,
		Height: 1200,
		PPI:    144,
	}
}

// ParseDisplaySpec parses a display specification string
// Formats:
//   - "1920x1080" - Width x Height at default PPI
//   - "1920x1080@144" - Width x Height at specific PPI
//   - "4k" - Preset for 3840x2160
//   - "1080p" - Preset for 1920x1080
//   - "720p" - Preset for 1280x720
func ParseDisplaySpec(s string) (DisplayConfig, error) {
	s = strings.ToLower(strings.TrimSpace(s))

	// Handle presets
	switch s {
	case "4k", "uhd":
		return DisplayConfig{Width: 3840, Height: 2160, PPI: 144}, nil
	case "1080p", "fhd":
		return DisplayConfig{Width: 1920, Height: 1080, PPI: 144}, nil
	case "720p", "hd":
		return DisplayConfig{Width: 1280, Height: 720, PPI: 144}, nil
	case "retina":
		return DisplayConfig{Width: 2560, Height: 1600, PPI: 227}, nil
	}

	config := DisplayConfig{PPI: 144} // Default PPI

	// Check for PPI specification
	if idx := strings.Index(s, "@"); idx > 0 {
		ppiStr := s[idx+1:]
		ppi, err := strconv.Atoi(ppiStr)
		if err != nil {
			return config, fmt.Errorf("invalid PPI value: %s", ppiStr)
		}
		config.PPI = ppi
		s = s[:idx]
	}

	// Parse WIDTHxHEIGHT
	parts := strings.Split(s, "x")
	if len(parts) != 2 {
		return config, fmt.Errorf("invalid display format: %s (expected WIDTHxHEIGHT)", s)
	}

	width, err := strconv.Atoi(parts[0])
	if err != nil {
		return config, fmt.Errorf("invalid width: %s", parts[0])
	}

	height, err := strconv.Atoi(parts[1])
	if err != nil {
		return config, fmt.Errorf("invalid height: %s", parts[1])
	}

	if width < 640 || width > 7680 {
		return config, fmt.Errorf("width must be between 640 and 7680, got %d", width)
	}
	if height < 480 || height > 4320 {
		return config, fmt.Errorf("height must be between 480 and 4320, got %d", height)
	}

	config.Width = width
	config.Height = height
	return config, nil
}

// DisplaySlice implements flag.Value for collecting multiple -display flags
type DisplaySlice []DisplayConfig

func (s *DisplaySlice) String() string {
	if s == nil || len(*s) == 0 {
		return ""
	}
	var parts []string
	for _, d := range *s {
		parts = append(parts, fmt.Sprintf("%dx%d@%d", d.Width, d.Height, d.PPI))
	}
	return strings.Join(parts, ",")
}

func (s *DisplaySlice) Set(value string) error {
	config, err := ParseDisplaySpec(value)
	if err != nil {
		return err
	}
	*s = append(*s, config)
	return nil
}

// CreateMacGraphicsConfig creates a macOS graphics device configuration
// with the specified displays (single or multiple)
func CreateMacGraphicsConfig(displays []DisplayConfig) (vz.VZMacGraphicsDeviceConfiguration, error) {
	if len(displays) == 0 {
		displays = []DisplayConfig{DefaultDisplayConfig()}
	}

	graphicsConfig := vz.NewVZMacGraphicsDeviceConfiguration()
	if graphicsConfig.ID == 0 {
		return graphicsConfig, fmt.Errorf("failed to create graphics device configuration")
	}

	// Create display configurations
	displayConfigs := make([]vz.VZMacGraphicsDisplayConfiguration, len(displays))
	for i, d := range displays {
		displayConfig := vz.NewMacGraphicsDisplayConfigurationWithWidthInPixelsHeightInPixelsPixelsPerInch(
			d.Width, d.Height, d.PPI)
		if displayConfig.ID == 0 {
			return graphicsConfig, fmt.Errorf("failed to create display configuration %d", i)
		}
		displayConfigs[i] = displayConfig
		if verbose {
			fmt.Printf("  Display %d: %dx%d @ %d PPI\n", i+1, d.Width, d.Height, d.PPI)
		}
	}

	graphicsConfig.SetDisplays(displayConfigs)
	return graphicsConfig, nil
}

// CreateVirtioGraphicsConfig creates a Linux/generic graphics device configuration
// with the specified displays (for VirtIO GPU)
func CreateVirtioGraphicsConfig(displays []DisplayConfig) (vz.VZVirtioGraphicsDeviceConfiguration, error) {
	if len(displays) == 0 {
		displays = []DisplayConfig{DefaultDisplayConfig()}
	}

	graphicsConfig := vz.NewVZVirtioGraphicsDeviceConfiguration()
	if graphicsConfig.ID == 0 {
		return graphicsConfig, fmt.Errorf("failed to create Virtio graphics device configuration")
	}

	// Create scanout configurations
	scanouts := make([]vz.VZVirtioGraphicsScanoutConfiguration, len(displays))
	for i, d := range displays {
		scanout := vz.NewVirtioGraphicsScanoutConfigurationWithWidthInPixelsHeightInPixels(
			d.Width, d.Height)
		if scanout.ID == 0 {
			return graphicsConfig, fmt.Errorf("failed to create scanout configuration %d", i)
		}
		scanouts[i] = scanout
		fmt.Printf("  Virtio Display %d: %dx%d\n", i+1, d.Width, d.Height)
	}

	graphicsConfig.SetScanouts(scanouts)
	return graphicsConfig, nil
}

// DisplayInfo contains runtime display information
type DisplayInfo struct {
	Index  int `json:"index"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// macOS VMs support up to 2 displays, Linux VMs typically support more
// We'll allow up to 4 displays

// GetDefaultDisplayForVM returns the appropriate default display for a VM type
func GetDefaultDisplayForVM(isLinux bool) DisplayConfig {
	if isLinux {
		// Linux VMs typically work better with standard resolutions
		return DisplayConfig{
			Width:  1920,
			Height: 1080,
			PPI:    144,
		}
	}
	// macOS VMs
	return DisplayConfig{
		Width:  1920,
		Height: 1200,
		PPI:    144,
	}
}
