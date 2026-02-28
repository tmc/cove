// ocr.go - Vision framework OCR for screen text recognition
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/vision"
)

// TextObservation holds a recognized text region from OCR.
type TextObservation struct {
	Text        string
	Confidence  float32
	BoundingBox corefoundation.CGRect // normalized coordinates (0-1), origin at bottom-left
	Center      image.Point           // center in screen pixel coordinates
}

// OCRService performs text recognition using Apple's Vision framework.
type OCRService struct {
	verbose bool
}

// NewOCRService creates a new OCR service.
func NewOCRService(verbose bool) *OCRService {
	return &OCRService{verbose: verbose}
}

// RecognizeText runs OCR on an image and returns all recognized text observations.
func (o *OCRService) RecognizeText(img image.Image) ([]TextObservation, error) {
	if img == nil {
		return nil, fmt.Errorf("nil image")
	}

	// Save image to temp file, load as CGImage via ImageIO
	tmpFile, err := os.CreateTemp("", "ocr-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if err := png.Encode(tmpFile, img); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("encode png: %w", err)
	}
	tmpFile.Close()

	return o.recognizeTextInFile(tmpFile.Name(), img.Bounds().Dx(), img.Bounds().Dy())
}

// FindText searches for text on screen and returns its center pixel coordinates.
// Returns found=false if the text is not visible.
func (o *OCRService) FindText(img image.Image, needle string) (x, y float64, found bool) {
	observations, err := o.RecognizeText(img)
	if err != nil {
		if o.verbose {
			fmt.Printf("[ocr] error: %v\n", err)
		}
		return 0, 0, false
	}

	needle = strings.ToLower(needle)
	for _, obs := range observations {
		if strings.Contains(strings.ToLower(obs.Text), needle) {
			if o.verbose {
				fmt.Printf("[ocr] found %q in %q at (%d, %d)\n", needle, obs.Text, obs.Center.X, obs.Center.Y)
			}
			return float64(obs.Center.X), float64(obs.Center.Y), true
		}
	}
	return 0, 0, false
}

// AllText returns all recognized text as a single string.
func (o *OCRService) AllText(img image.Image) string {
	observations, err := o.RecognizeText(img)
	if err != nil {
		return ""
	}
	var lines []string
	for _, obs := range observations {
		lines = append(lines, obs.Text)
	}
	return strings.Join(lines, "\n")
}

func (o *OCRService) recognizeTextInFile(path string, width, height int) ([]TextObservation, error) {
	url := foundation.GetNSURLClass().FileURLWithPath(path)
	if url.ID == 0 {
		return nil, fmt.Errorf("invalid path: %s", path)
	}

	handler := vision.NewImageRequestHandlerWithURLOptions(url, nil)

	request := vision.NewVNRecognizeTextRequest()
	request.SetRecognitionLevel(vision.VNRequestTextRecognitionLevelAccurate)
	request.SetUsesLanguageCorrection(true)

	ok, err := handler.PerformRequestsError([]vision.VNRequest{
		vision.VNRequestFromID(request.ID),
	})
	if err != nil {
		return nil, fmt.Errorf("perform requests: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("perform requests returned false")
	}

	return o.extractResults(request, width, height), nil
}

func (o *OCRService) extractResults(request vision.VNRecognizeTextRequest, width, height int) []TextObservation {
	observations := request.Results()
	var results []TextObservation
	for _, obs := range observations {
		textObs := vision.VNRecognizedTextObservationFromID(obs.ID)
		candidates := textObs.TopCandidates(1)
		if len(candidates) == 0 {
			continue
		}
		candidate := candidates[0]
		bbox := textObs.BoundingBox()

		// Convert normalized bbox (origin bottom-left) to pixel center
		centerX := int((bbox.Origin.X + bbox.Size.Width/2) * float64(width))
		centerY := int((1 - bbox.Origin.Y - bbox.Size.Height/2) * float64(height))

		result := TextObservation{
			Text:        candidate.String(),
			Confidence:  float32(candidate.Confidence()),
			BoundingBox: bbox,
			Center:      image.Point{X: centerX, Y: centerY},
		}
		results = append(results, result)

		if o.verbose {
			fmt.Printf("[ocr] [%.2f] %q at (%d,%d)\n", result.Confidence, result.Text, centerX, centerY)
		}
	}
	return results
}

// Get CGImage from the window for direct OCR (avoids PNG round-trip)

// Fallback to PNG round-trip
