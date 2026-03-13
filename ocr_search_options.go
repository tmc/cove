package main

import (
	"fmt"
	"image"
	"strconv"
	"strings"
)

// OCRRegion describes a normalized screen rectangle (0-1, top-left origin).
type OCRRegion struct {
	MinX float64
	MinY float64
	MaxX float64
	MaxY float64
}

// OCRSearchOptions controls OCR text matching behavior.
type OCRSearchOptions struct {
	Region    *OCRRegion
	PreferTop bool
}

var ocrMenuBarRegion = OCRRegion{
	MinX: 0,
	MinY: 0,
	MaxX: 1,
	MaxY: 0.30,
}

// OCRMenuSearchOptions returns options tuned for menu bar targeting.
func OCRMenuSearchOptions() OCRSearchOptions {
	region := ocrMenuBarRegion
	return OCRSearchOptions{
		Region:    &region,
		PreferTop: true,
	}
}

// ParseOCRSearchOptions parses a region selector for OCR commands.
// Supported selectors:
//   - "" / "screen" / "full": whole screen
//   - "menu" / "menubar": top menu bar strip
//   - "x1,y1,x2,y2": normalized rectangle coordinates
func ParseOCRSearchOptions(regionSpec string) (OCRSearchOptions, error) {
	spec := strings.TrimSpace(strings.ToLower(regionSpec))
	switch spec {
	case "", "screen", "full":
		return OCRSearchOptions{}, nil
	case "menu", "menubar", "top-menu":
		return OCRMenuSearchOptions(), nil
	}

	parts := strings.Split(spec, ",")
	if len(parts) != 4 {
		return OCRSearchOptions{}, fmt.Errorf("invalid OCR region %q (want menu or x1,y1,x2,y2)", regionSpec)
	}

	values := make([]float64, 0, 4)
	for _, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return OCRSearchOptions{}, fmt.Errorf("invalid OCR region %q: %w", regionSpec, err)
		}
		values = append(values, v)
	}

	region := OCRRegion{
		MinX: values[0],
		MinY: values[1],
		MaxX: values[2],
		MaxY: values[3],
	}
	if err := validateOCRRegion(region); err != nil {
		return OCRSearchOptions{}, fmt.Errorf("invalid OCR region %q: %w", regionSpec, err)
	}
	return OCRSearchOptions{Region: &region}, nil
}

func validateOCRRegion(region OCRRegion) error {
	if region.MinX < 0 || region.MinX > 1 || region.MaxX < 0 || region.MaxX > 1 {
		return fmt.Errorf("x coordinates must be within [0,1]")
	}
	if region.MinY < 0 || region.MinY > 1 || region.MaxY < 0 || region.MaxY > 1 {
		return fmt.Errorf("y coordinates must be within [0,1]")
	}
	if region.MinX >= region.MaxX {
		return fmt.Errorf("x1 must be less than x2")
	}
	if region.MinY >= region.MaxY {
		return fmt.Errorf("y1 must be less than y2")
	}
	return nil
}

func observationInSearchRegion(obs TextObservation, bounds image.Rectangle, region *OCRRegion) bool {
	if region == nil {
		return true
	}
	width := bounds.Dx()
	height := bounds.Dy()
	if width == 0 || height == 0 {
		return false
	}
	x := float64(obs.Center.X-bounds.Min.X) / float64(width)
	y := float64(obs.Center.Y-bounds.Min.Y) / float64(height)
	return x >= region.MinX && x <= region.MaxX && y >= region.MinY && y <= region.MaxY
}
