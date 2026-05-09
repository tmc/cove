package main

import (
	"image"
	"testing"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
)

func TestOCRParseSearchOptions(t *testing.T) {
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
			opts, err := ocrx.ParseSearchOptions(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ocrx.ParseSearchOptions(%q) err=%v wantErr=%v", tt.spec, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if (opts.Region != nil) != tt.wantRegion {
				t.Fatalf("ocrx.ParseSearchOptions(%q) region nil=%v wantRegion=%v", tt.spec, opts.Region == nil, tt.wantRegion)
			}
			if opts.PreferTop != tt.wantTop {
				t.Fatalf("ocrx.ParseSearchOptions(%q) preferTop=%v want=%v", tt.spec, opts.PreferTop, tt.wantTop)
			}
		})
	}
}

func TestBestMatchWithOptions(t *testing.T) {
	observations := []ocrx.TextObservation{
		{Text: "Utilities", Center: image.Point{X: 100, Y: 50}},  // top
		{Text: "Utilities", Center: image.Point{X: 100, Y: 500}}, // bottom
	}
	bounds := image.Rect(0, 0, 1000, 1000)

	got, ok := ocrx.BestMatch(observations, "Utilities", ocrx.SearchOptions{}, bounds)
	if !ok {
		t.Fatal("ocrx.BestMatch() no match")
	}
	if got.Center.Y != 500 {
		t.Fatalf("default match Y=%d want 500 (bottom bias)", got.Center.Y)
	}

	menuOpts := ocrx.MenuSearchOptions()
	got, ok = ocrx.BestMatch(observations, "Utilities", menuOpts, bounds)
	if !ok {
		t.Fatal("ocrx.BestMatch(menu) no match")
	}
	if got.Center.Y != 50 {
		t.Fatalf("menu match Y=%d want 50 (top bias)", got.Center.Y)
	}
}

func TestObservationInSearchRegion(t *testing.T) {
	bounds := image.Rect(0, 0, 1000, 1000)
	top := ocrx.TextObservation{Center: image.Point{X: 500, Y: 50}}
	bottom := ocrx.TextObservation{Center: image.Point{X: 500, Y: 900}}
	opts := ocrx.MenuSearchOptions()

	if !observationInSearchRegion(top, bounds, opts.Region) {
		t.Fatal("top point should be inside menu region")
	}
	if observationInSearchRegion(bottom, bounds, opts.Region) {
		t.Fatal("bottom point should be outside menu region")
	}
}

func TestObservationInSearchRegion_Cases(t *testing.T) {
	region := &ocrx.Region{MinX: 0.25, MinY: 0.25, MaxX: 0.75, MaxY: 0.75}
	tests := []struct {
		name   string
		bounds image.Rectangle
		region *ocrx.Region
		pt     image.Point
		want   bool
	}{
		{"nil region", image.Rect(0, 0, 100, 100), nil, image.Point{X: 0, Y: 0}, true},
		{"zero width", image.Rect(0, 0, 0, 100), region, image.Point{X: 50, Y: 50}, false},
		{"zero height", image.Rect(0, 0, 100, 0), region, image.Point{X: 50, Y: 50}, false},
		{"inside center", image.Rect(0, 0, 100, 100), region, image.Point{X: 50, Y: 50}, true},
		{"outside left", image.Rect(0, 0, 100, 100), region, image.Point{X: 10, Y: 50}, false},
		{"min boundary", image.Rect(0, 0, 100, 100), region, image.Point{X: 25, Y: 25}, true},
		{"max boundary", image.Rect(0, 0, 100, 100), region, image.Point{X: 75, Y: 75}, true},
		{"translated bounds inside", image.Rect(200, 200, 300, 300), region, image.Point{X: 250, Y: 250}, true},
		{"translated bounds outside", image.Rect(200, 200, 300, 300), region, image.Point{X: 210, Y: 210}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := ocrx.TextObservation{Center: tt.pt}
			got := observationInSearchRegion(obs, tt.bounds, tt.region)
			if got != tt.want {
				t.Fatalf("observationInSearchRegion(%v) = %v, want %v", tt.pt, got, tt.want)
			}
		})
	}
}
