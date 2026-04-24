// ocr_search_options.go - OCR search region and options.
//
// Delegates to github.com/tmc/apple/x/vzkit/ocr for the implementation.
package main

import (
	"image"

	"github.com/tmc/apple/x/vzkit/ocr"
)

// OCRMenuSearchOptions returns options tuned for menu bar targeting.
func OCRMenuSearchOptions() ocr.SearchOptions {
	return ocr.MenuSearchOptions()
}

// ParseOCRSearchOptions parses a region selector for OCR commands.
func ParseOCRSearchOptions(regionSpec string) (ocr.SearchOptions, error) {
	return ocr.ParseSearchOptions(regionSpec)
}

// observationInSearchRegion checks if an observation falls within the search region.
func observationInSearchRegion(obs ocr.TextObservation, bounds image.Rectangle, region *ocr.Region) bool {
	// Use BestMatch with a single observation to check region membership.
	// This is a simple proxy since observationInRegion is unexported in vzkit/ocr.
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
