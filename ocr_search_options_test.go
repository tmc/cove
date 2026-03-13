package main

import (
	"image"
	"testing"
)

func TestParseOCRSearchOptions(t *testing.T) {
	tests := []struct {
		name       string
		spec       string
		wantRegion bool
		wantTop    bool
		wantErr    bool
	}{
		{name: "empty", spec: "", wantRegion: false},
		{name: "menu", spec: "menu", wantRegion: true, wantTop: true},
		{name: "rect", spec: "0.1,0.2,0.9,0.5", wantRegion: true},
		{name: "invalid", spec: "0.1,0.2,0.9", wantErr: true},
		{name: "invalid bounds", spec: "0.9,0.2,0.1,0.5", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := ParseOCRSearchOptions(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseOCRSearchOptions(%q) err=%v wantErr=%v", tt.spec, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if (opts.Region != nil) != tt.wantRegion {
				t.Fatalf("ParseOCRSearchOptions(%q) region nil=%v wantRegion=%v", tt.spec, opts.Region == nil, tt.wantRegion)
			}
			if opts.PreferTop != tt.wantTop {
				t.Fatalf("ParseOCRSearchOptions(%q) preferTop=%v want=%v", tt.spec, opts.PreferTop, tt.wantTop)
			}
		})
	}
}

func TestBestMatchWithOptions(t *testing.T) {
	observations := []TextObservation{
		{Text: "Utilities", Center: image.Point{X: 100, Y: 50}},  // top
		{Text: "Utilities", Center: image.Point{X: 100, Y: 500}}, // bottom
	}
	bounds := image.Rect(0, 0, 1000, 1000)

	got, ok := bestMatchWithOptions(observations, "Utilities", OCRSearchOptions{}, bounds)
	if !ok {
		t.Fatal("bestMatchWithOptions() no match")
	}
	if got.Center.Y != 500 {
		t.Fatalf("default match Y=%d want 500 (bottom bias)", got.Center.Y)
	}

	menuOpts := OCRMenuSearchOptions()
	got, ok = bestMatchWithOptions(observations, "Utilities", menuOpts, bounds)
	if !ok {
		t.Fatal("bestMatchWithOptions(menu) no match")
	}
	if got.Center.Y != 50 {
		t.Fatalf("menu match Y=%d want 50 (top bias)", got.Center.Y)
	}
}

func TestObservationInSearchRegion(t *testing.T) {
	bounds := image.Rect(0, 0, 1000, 1000)
	top := TextObservation{Center: image.Point{X: 500, Y: 50}}
	bottom := TextObservation{Center: image.Point{X: 500, Y: 900}}
	opts := OCRMenuSearchOptions()

	if !observationInSearchRegion(top, bounds, opts.Region) {
		t.Fatal("top point should be inside menu region")
	}
	if observationInSearchRegion(bottom, bounds, opts.Region) {
		t.Fatal("bottom point should be outside menu region")
	}
}
