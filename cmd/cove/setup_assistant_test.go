package main

import (
	"image"
	"math"
	"testing"

	"github.com/tmc/apple/corefoundation"
	ocrx "github.com/tmc/apple/x/vzkit/ocr"
)

func TestAccountNameFieldPoint(t *testing.T) {
	tests := []struct {
		name               string
		width, height, row int
		labelX, labelWidth float64
		wantX, wantY       float64
	}{
		{"label beside field", 1024, 852, 409, .29, .05, .5, 409.0 / 852},
		{"placeholder inside field", 1920, 1200, 640, .46, .08, .57, 640.0 / 1200},
		{"scaled capture", 512, 426, 204, .29, .05, .5, 204.0 / 426},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := []ocrx.TextObservation{{Text: "Full Name", Center: image.Pt(tt.width/3, tt.row), BoundingBox: corefoundation.CGRect{Origin: corefoundation.CGPoint{X: tt.labelX}, Size: corefoundation.CGSize{Width: tt.labelWidth}}}}
			x, y, err := accountNameFieldPoint(obs, image.Rect(0, 0, tt.width, tt.height))
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(x-tt.wantX) > 1e-6 || math.Abs(y-tt.wantY) > 1e-6 {
				t.Fatalf("point = (%g, %g), want (%g, %g)", x, y, tt.wantX, tt.wantY)
			}
		})
	}
	if _, _, err := accountNameFieldPoint(nil, image.Rect(0, 0, 1024, 852)); err == nil {
		t.Fatal("missing label accepted")
	}
}
