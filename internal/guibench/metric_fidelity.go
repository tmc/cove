package guibench

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // register JPEG decoder for image_similar
	_ "image/png"  // register PNG decoder for image_similar
	"net/url"
	"sort"
	"strings"
)

// The fidelity metrics tolerate the lossy transforms a native-macOS edit
// applies — Preview/Photos re-encode an image and rewrite EXIF, a Safari
// navigation appends tracking query params, a PDF export reflows whitespace —
// so a verifier that compared raw bytes (hash_equals) or exact URLs (url_in)
// would report a false negative on a correct agent action. Each is a pure
// function over the value a getter already extracted (image bytes, the
// agent-reported URL, getter-extracted PDF text); none touches a VM, the
// network, or the clock (design 047 §5). They mirror OSWorld's tolerant
// comparison (metrics/gimp.py structure_check_by_mse, metrics/chrome.py
// is_expected_active_tab_approximate).

// metricImageSimilar scores 1 iff result and expected decode to images that are
// structurally the same within a tolerance, and 0 otherwise. result and
// expected are raw image bytes (PNG or JPEG) carried as strings — the file
// getter yields image bytes via string(b), so a Preview/Photos edit verifies
// despite the EXIF rewrite and recompression that break hash_equals.
//
// Similarity is a normalized per-pixel mean-squared error: each image is
// decoded, converted to RGBA, the result is resized by nearest-neighbour to the
// expected's dimensions when they differ (the OSWorld resize-to-match rule, so a
// Retina re-render compares against a smaller gold), and the score is
// 1 - mean((a-b)^2) over the four channels normalized to [0,1]. The pass
// threshold is the "tolerance" option (default 0.03 MSE, matching OSWorld's
// gimp.py default): mse < tolerance scores 1, else 0. This is intentionally a
// thresholded MSE rather than a windowed SSIM — it is stdlib-only (no
// scikit-image dependency), deterministic, and sufficient for "this is the same
// picture after a lossy re-encode" which is the macOS-edit case the corpus needs
// (design 047 §5). A "graded" option returns the raw similarity in [0,1] instead
// of the thresholded pass/fail, for tasks that want partial credit.
func metricImageSimilar(result, expected string, options map[string]any) (float64, error) {
	tolerance, err := floatOption(options, "tolerance", 0.03)
	if err != nil {
		return 0, fmt.Errorf("image_similar: %w", err)
	}
	graded, err := boolOption(options, "graded", false)
	if err != nil {
		return 0, fmt.Errorf("image_similar: %w", err)
	}
	a, err := decodeRGBA([]byte(result))
	if err != nil {
		return 0, fmt.Errorf("image_similar: result image: %w", err)
	}
	b, err := decodeRGBA([]byte(expected))
	if err != nil {
		return 0, fmt.Errorf("image_similar: expected image: %w", err)
	}
	mse := imageMSE(a, b)
	if graded {
		return 1 - mse, nil
	}
	return score(mse < tolerance), nil
}

// metricPDFContains scores 1 iff the gold expected text is present in the
// getter-extracted PDF text, under fuzzy normalization (lowercase, trimmed,
// whitespace-collapsed). result is the PDF's text content, which the getter
// extracted guest-side (see the getter command in the package docs); expected
// is the required phrase. This reuses [normalize] so a PDF export's reflowed
// line breaks and indentation do not defeat a correct content match, mirroring
// WebArena's must_include with fuzzy semantics. When the "exact_phrase" option
// is set, the comparison is a substring check WITHOUT normalization, for text
// that is whitespace-significant.
func metricPDFContains(result, expected string, options map[string]any) (float64, error) {
	if expected == "" {
		return 0, fmt.Errorf("pdf_contains: expected text is empty")
	}
	exact, err := boolOption(options, "exact_phrase", false)
	if err != nil {
		return 0, fmt.Errorf("pdf_contains: %w", err)
	}
	if exact {
		return score(strings.Contains(result, expected)), nil
	}
	return score(strings.Contains(normalize(result), normalize(expected))), nil
}

// metricURLMatchNormalized scores 1 iff result and expected name the same page
// after stripping tracking/session noise: scheme is dropped, host is lowercased
// with a leading "www." removed, the path's trailing slash is dropped, and the
// query, fragment, and userinfo are discarded entirely. This mirrors OSWorld's
// is_expected_active_tab_approximate (strip the query, compare the rest) and
// the WebArena/getters/chrome.py intent of normalizing before comparing, so a
// Safari navigation that picked up "?utm_source=..." still matches the gold URL.
//
// When the "keep_query" option lists param names, those query params are
// retained (sorted) in the comparison — for the rare task where a query value
// is load-bearing (a search term, a page number).
func metricURLMatchNormalized(result, expected string, options map[string]any) (float64, error) {
	if expected == "" {
		return 0, fmt.Errorf("url_match_normalized: expected url is empty")
	}
	keep, err := stringSliceOption(options, "keep_query")
	if err != nil {
		return 0, fmt.Errorf("url_match_normalized: %w", err)
	}
	return score(canonicalURL(result, keep) == canonicalURL(expected, keep)), nil
}

// decodeRGBA decodes PNG or JPEG bytes into an *image.RGBA, so pixel access is
// O(1) and channel order is fixed regardless of the source color model.
func decodeRGBA(b []byte) (*image.RGBA, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("empty image")
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Src)
	return dst, nil
}

// imageMSE returns the mean squared error in [0,1] between two RGBA images over
// all four channels. When the images differ in size, a is resized to b's
// dimensions by nearest-neighbour first (the OSWorld resize-to-match rule),
// because a correct edit may be re-rendered at a different scale.
func imageMSE(a, b *image.RGBA) float64 {
	if a.Rect.Dx() != b.Rect.Dx() || a.Rect.Dy() != b.Rect.Dy() {
		a = resizeNearest(a, b.Rect.Dx(), b.Rect.Dy())
	}
	w, h := b.Rect.Dx(), b.Rect.Dy()
	if w == 0 || h == 0 {
		return 1 // an empty target cannot match; report maximal error
	}
	var sum float64
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ca := a.RGBAAt(x, y)
			cb := b.RGBAAt(x, y)
			sum += sq(ca.R, cb.R) + sq(ca.G, cb.G) + sq(ca.B, cb.B) + sq(ca.A, cb.A)
		}
	}
	// Four channels, each normalized to [0,1] (divide by 255 before squaring,
	// i.e. divide the squared sum by 255^2), averaged over pixels*channels.
	return sum / (float64(w) * float64(h) * 4 * 255 * 255)
}

// sq returns the squared difference of two 8-bit channel values as a float64.
func sq(p, q uint8) float64 {
	d := float64(p) - float64(q)
	return d * d
}

// resizeNearest returns src resized to w×h by nearest-neighbour sampling. It is
// the cheapest size-reconciliation that keeps the metric stdlib-only and
// deterministic; the corpus uses it only to reconcile scale, not to judge a
// resize, so sample quality does not affect the verdict.
func resizeNearest(src *image.RGBA, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if sw == 0 || sh == 0 || w == 0 || h == 0 {
		return dst
	}
	for y := 0; y < h; y++ {
		sy := y * sh / h
		for x := 0; x < w; x++ {
			sx := x * sw / w
			dst.SetRGBA(x, y, src.RGBAAt(sx, sy))
		}
	}
	return dst
}

// canonicalURL reduces a URL to a comparison key that ignores tracking and
// session noise: host (lowercased, leading "www." removed) plus path (trailing
// slash dropped), plus any query params named in keep (sorted). Scheme,
// fragment, userinfo, port, and all other query params are discarded. A URL
// that does not parse is returned trimmed and lowercased, so a malformed value
// still compares deterministically rather than erroring.
func canonicalURL(raw string, keep []string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimRight(raw, "/"))
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	path := strings.TrimRight(u.Path, "/")
	key := host + path
	if len(keep) > 0 {
		if q := keptQuery(u.Query(), keep); q != "" {
			key += "?" + q
		}
	}
	return key
}

// keptQuery returns the sorted "k=v" join of exactly the query params named in
// keep, so a load-bearing query value survives normalization while tracking
// params are still dropped.
func keptQuery(values url.Values, keep []string) string {
	var parts []string
	for _, k := range keep {
		for _, v := range values[k] {
			parts = append(parts, k+"="+v)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "&")
}

// floatOption reads a numeric option, returning def when absent. It accepts
// float64 (the JSON number type) and int (a Go literal in a test), so a
// tolerance written as 0.03 in JSON or 0 in a struct literal both work.
func floatOption(options map[string]any, key string, def float64) (float64, error) {
	v, ok := options[key]
	if !ok {
		return def, nil
	}
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	default:
		return 0, fmt.Errorf("option %q: want number, got %T", key, v)
	}
}

// stringSliceOption reads a []string option from a JSON-decoded value ([]any of
// strings) or a Go []string literal, returning nil when absent.
func stringSliceOption(options map[string]any, key string) ([]string, error) {
	v, ok := options[key]
	if !ok {
		return nil, nil
	}
	switch s := v.(type) {
	case []string:
		return s, nil
	case []any:
		out := make([]string, 0, len(s))
		for i, e := range s {
			str, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("option %q[%d]: want string, got %T", key, i, e)
			}
			out = append(out, str)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("option %q: want []string, got %T", key, v)
	}
}
