package main

import (
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/apple/corefoundation"
)

func TestSaveOCRDebugScreenshot(t *testing.T) {
	dir := t.TempDir()
	img := newSolidImage(200, 200, color.RGBA{100, 100, 100, 255})

	observations := []TextObservation{
		{
			Text:       "Hello",
			Confidence: 0.95,
			BoundingBox: corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: 0.1, Y: 0.1},
				Size:   corefoundation.CGSize{Width: 0.3, Height: 0.1},
			},
			Center: image.Point{X: 50, Y: 170},
		},
		{
			Text:       "World",
			Confidence: 0.88,
			BoundingBox: corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: 0.5, Y: 0.5},
				Size:   corefoundation.CGSize{Width: 0.2, Height: 0.08},
			},
			Center: image.Point{X: 120, Y: 92},
		},
	}

	err := saveOCRDebugScreenshot(img, observations, dir, "test")
	if err != nil {
		t.Fatalf("saveOCRDebugScreenshot: %v", err)
	}

	// Check that PNG and JSON files were created
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}

	var pngFound, jsonFound bool
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".png" {
			pngFound = true
		}
		if filepath.Ext(e.Name()) == ".json" {
			jsonFound = true
			// Verify JSON structure
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read json: %v", err)
			}
			var entry OCRDebugEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				t.Fatalf("unmarshal json: %v", err)
			}
			if entry.Step != "test" {
				t.Errorf("entry.Step = %q, want %q", entry.Step, "test")
			}
			if len(entry.Observations) != 2 {
				t.Errorf("entry.Observations = %d, want 2", len(entry.Observations))
			}
		}
	}

	if !pngFound {
		t.Error("no PNG file created")
	}
	if !jsonFound {
		t.Error("no JSON file created")
	}
}

func TestSaveOCRDebugScreenshot_EmptyObservations(t *testing.T) {
	dir := t.TempDir()
	img := newSolidImage(100, 100, color.RGBA{50, 50, 50, 255})

	err := saveOCRDebugScreenshot(img, nil, dir, "empty")
	if err != nil {
		t.Fatalf("saveOCRDebugScreenshot with nil observations: %v", err)
	}
}

func TestSaveOCRDebugScreenshot_CreatesDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "nested", "debug")
	img := newSolidImage(100, 100, color.RGBA{50, 50, 50, 255})

	err := saveOCRDebugScreenshot(img, nil, dir, "nested")
	if err != nil {
		t.Fatalf("saveOCRDebugScreenshot with nested dir: %v", err)
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("nested directory was not created")
	}
}

func TestDrawRect(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	green := color.RGBA{0, 255, 0, 200}

	// Should not panic
	drawRect(img, 10, 10, 50, 50, green)

	// Verify a corner pixel was drawn
	got := img.RGBAAt(10, 10)
	if got != green {
		t.Errorf("corner pixel = %v, want %v", got, green)
	}

	// Verify interior pixel was not drawn
	interior := img.RGBAAt(30, 30)
	if interior == green {
		t.Error("interior pixel should not be drawn by drawRect")
	}
}

func TestDrawRect_Clamping(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	green := color.RGBA{0, 255, 0, 200}

	// Should not panic with out-of-bounds coords
	drawRect(img, -5, -5, 100, 100, green)
}
