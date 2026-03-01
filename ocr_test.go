package main

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestOCRService_RecognizeText_NilImage(t *testing.T) {
	ocr := NewOCRService(false)
	_, err := ocr.RecognizeText(nil)
	if err == nil {
		t.Fatal("expected error for nil image")
	}
}

func TestOCRService_RecognizeText_BlankImage(t *testing.T) {
	ocr := NewOCRService(false)
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	observations, err := ocr.RecognizeText(img)
	if err != nil {
		t.Fatalf("unexpected error on blank image: %v", err)
	}
	// A blank white image should have no text observations
	if len(observations) != 0 {
		t.Errorf("expected 0 observations on blank image, got %d", len(observations))
	}
}

func TestOCRService_FindText_NilImage(t *testing.T) {
	ocr := NewOCRService(false)
	_, _, found := ocr.FindText(nil, "hello")
	if found {
		t.Error("expected found=false for nil image")
	}
}

func TestOCRService_AllText_NilImage(t *testing.T) {
	ocr := NewOCRService(false)
	result := ocr.AllText(nil)
	if result != "" {
		t.Errorf("expected empty string for nil image, got %q", result)
	}
}

func TestOCRService_FindText_CaseInsensitive(t *testing.T) {
	// FindText should be case-insensitive (it lowercases the needle)
	ocr := NewOCRService(false)
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))

	// With a blank image, nothing should be found regardless of case
	_, _, found := ocr.FindText(img, "HELLO")
	if found {
		t.Error("expected found=false for blank image")
	}
}

func TestOCRService_Verbose(t *testing.T) {
	// Verify verbose mode doesn't panic
	ocr := NewOCRService(true)
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	ocr.RecognizeText(img)
	ocr.FindText(img, "test")
	ocr.AllText(img)
}

func TestOCRService_FindTextNormalized_NilImage(t *testing.T) {
	ocr := NewOCRService(false)
	_, _, found := ocr.FindTextNormalized(nil, "hello")
	if found {
		t.Error("expected found=false for nil image")
	}
}

func TestOCRService_FindTextNormalized_BlankImage(t *testing.T) {
	ocr := NewOCRService(false)
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	_, _, found := ocr.FindTextNormalized(img, "hello")
	if found {
		t.Error("expected found=false for blank image")
	}
}

func TestOCRService_FindAllTextNormalized_BlankImage(t *testing.T) {
	ocr := NewOCRService(false)
	img := image.NewRGBA(image.Rect(0, 0, 200, 200))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

	results, err := ocr.FindAllTextNormalized(img)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results on blank image, got %d", len(results))
	}
}

func TestOCRService_FindAllTextNormalized_NilImage(t *testing.T) {
	ocr := NewOCRService(false)
	_, err := ocr.FindAllTextNormalized(nil)
	if err == nil {
		t.Error("expected error for nil image")
	}
}
