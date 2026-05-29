package guibench

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"testing"
)

// solidPNG returns the PNG encoding of a w×h image filled with c. Test images
// are generated here so no binary fixtures are committed (design 047 §9, repo
// rule: no committed PNGs).
func solidPNG(t *testing.T, w, h int, c color.Color) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.String()
}

// gradientJPEG returns the JPEG encoding of a w×h horizontal gray gradient at
// the given quality, used to exercise the lossy-recompression tolerance the
// metric exists for (a Preview/Photos re-export).
func gradientJPEG(t *testing.T, w, h, quality int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(x * 255 / max1(w-1))
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.String()
}

// gradientPNG is the lossless reference for gradientJPEG: the same gray
// gradient encoded as PNG, so a JPEG re-encode of the gradient compares against
// a pristine gold.
func gradientPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(x * 255 / max1(w-1))
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.String()
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func TestMetricImageSimilar(t *testing.T) {
	white := solidPNG(t, 16, 16, color.RGBA{255, 255, 255, 255})
	whiteAgain := solidPNG(t, 16, 16, color.RGBA{255, 255, 255, 255})
	black := solidPNG(t, 16, 16, color.RGBA{0, 0, 0, 255})
	// Same picture, re-encoded as a smaller image: the resize-to-match path
	// must still recognize it as identical.
	whiteSmall := solidPNG(t, 8, 8, color.RGBA{255, 255, 255, 255})
	// Lossy JPEG re-encode of a gradient vs. its lossless PNG: hash_equals
	// would fail; image_similar must pass under the default tolerance.
	gradPNG := gradientPNG(t, 32, 32)
	gradJPEG := gradientJPEG(t, 32, 32, 80)

	tests := []struct {
		name     string
		result   string
		expected string
		options  map[string]any
		want     float64
		wantErr  bool
	}{
		{"identical png", white, whiteAgain, nil, 1, false},
		{"identical resized", white, whiteSmall, nil, 1, false},
		{"opposite fails", white, black, nil, 0, false},
		{"jpeg recompress tolerated", gradJPEG, gradPNG, nil, 1, false},
		{"tight tolerance rejects recompress", gradJPEG, gradPNG, map[string]any{"tolerance": 0.0000001}, 0, false},
		{"loose tolerance accepts mismatch", white, black, map[string]any{"tolerance": 2.0}, 1, false},
		{"bad result bytes", "not an image", white, nil, 0, true},
		{"bad expected bytes", white, "not an image", nil, 0, true},
		{"empty result", "", white, nil, 0, true},
		{"bad tolerance type", white, white, map[string]any{"tolerance": "loose"}, 0, true},
		{"bad graded type", white, white, map[string]any{"graded": "yes"}, 0, true},
	}
	m := Metrics()["image_similar"]
	if m == nil {
		t.Fatal("image_similar not registered")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m(tt.result, tt.expected, tt.options)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got score %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMetricImageSimilarGraded(t *testing.T) {
	// graded mode returns the raw similarity: identical -> 1, opposite -> 0.
	white := solidPNG(t, 8, 8, color.RGBA{255, 255, 255, 255})
	black := solidPNG(t, 8, 8, color.RGBA{0, 0, 0, 255})
	m := Metrics()["image_similar"]

	got, err := m(white, white, map[string]any{"graded": true})
	if err != nil {
		t.Fatalf("graded identical: %v", err)
	}
	if math.Abs(got-1) > 1e-9 {
		t.Fatalf("graded identical = %v, want 1", got)
	}
	got, err = m(white, black, map[string]any{"graded": true})
	if err != nil {
		t.Fatalf("graded opposite: %v", err)
	}
	// white vs black: every RGB channel maxes out; alpha matches. MSE over four
	// channels = 3/4, similarity = 1/4.
	if math.Abs(got-0.25) > 1e-9 {
		t.Fatalf("graded opposite = %v, want 0.25", got)
	}
}

func TestMetricPDFContains(t *testing.T) {
	const extracted = "Invoice  #4271\nTotal   Due:   $1,200.00\n  Thank   you  for  your  business."
	tests := []struct {
		name     string
		result   string
		expected string
		options  map[string]any
		want     float64
		wantErr  bool
	}{
		{"phrase present fuzzy", extracted, "total due: $1,200.00", nil, 1, false},
		{"reflowed whitespace tolerated", extracted, "Thank you for your business.", nil, 1, false},
		{"phrase absent", extracted, "refund issued", nil, 0, false},
		{"exact phrase requires literal spacing", extracted, "Invoice #4271", map[string]any{"exact_phrase": true}, 0, false},
		{"exact phrase literal hit", extracted, "Invoice  #4271", map[string]any{"exact_phrase": true}, 1, false},
		{"empty expected errors", extracted, "", nil, 0, true},
		{"bad option type", extracted, "x", map[string]any{"exact_phrase": "yes"}, 0, true},
	}
	m := Metrics()["pdf_contains"]
	if m == nil {
		t.Fatal("pdf_contains not registered")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m(tt.result, tt.expected, tt.options)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got score %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMetricURLMatchNormalized(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		expected string
		options  map[string]any
		want     float64
		wantErr  bool
	}{
		{"strips tracking query", "https://example.com/page?utm_source=news&ref=abc", "https://example.com/page", nil, 1, false},
		{"strips fragment", "https://example.com/docs#section-3", "https://example.com/docs", nil, 1, false},
		{"drops www and scheme", "http://www.example.com/a", "https://example.com/a", nil, 1, false},
		{"trailing slash equal", "https://example.com/a/", "https://example.com/a", nil, 1, false},
		{"different host fails", "https://other.com/a", "https://example.com/a", nil, 0, false},
		{"different path fails", "https://example.com/b", "https://example.com/a", nil, 0, false},
		{"kept query matches", "https://example.com/s?q=cats&utm_source=x", "https://example.com/s?q=cats", map[string]any{"keep_query": []string{"q"}}, 1, false},
		{"kept query mismatch", "https://example.com/s?q=dogs", "https://example.com/s?q=cats", map[string]any{"keep_query": []string{"q"}}, 0, false},
		{"kept query from json slice", "https://example.com/s?q=cats", "https://example.com/s?q=cats&utm=z", map[string]any{"keep_query": []any{"q"}}, 1, false},
		{"empty expected errors", "https://example.com", "", nil, 0, true},
		{"bad keep_query type", "https://example.com", "https://example.com", map[string]any{"keep_query": "q"}, 0, true},
		{"bad keep_query element", "https://example.com", "https://example.com", map[string]any{"keep_query": []any{7}}, 0, true},
	}
	m := Metrics()["url_match_normalized"]
	if m == nil {
		t.Fatal("url_match_normalized not registered")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m(tt.result, tt.expected, tt.options)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got score %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanonicalURL(t *testing.T) {
	// The canonical key drops scheme, www, port, userinfo, query, and fragment.
	tests := []struct {
		raw  string
		want string
	}{
		{"https://www.Example.com:443/Path/?a=1#frag", "example.com/Path"},
		{"https://example.com/", "example.com"},
		{"  https://example.com/x/  ", "example.com/x"},
		{"not a url", "not a url"},
	}
	for _, tt := range tests {
		if got := canonicalURL(tt.raw, nil); got != tt.want {
			t.Errorf("canonicalURL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
