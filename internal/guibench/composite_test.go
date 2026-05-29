package guibench

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

func compositeWhitePNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.String()
}

// TestCompositeEvaluate scores a multi-getter composite: one check reads a
// Messages-style SQLite row, the other compares an exported Preview image.
// Each check has its OWN getter and metric, which the built-in single-Result
// Evaluator cannot express; "and" means both artifacts must be right.
func TestCompositeEvaluate(t *testing.T) {
	whiteImg := compositeWhitePNG(t)
	probe := FakeProbe{
		Files: map[string]string{
			"/Users/tmc/Desktop/out.png": whiteImg,
		},
		Commands: map[string]ExecResult{
			"sqlite3 /Users/tmc/Library/Messages/chat.db PRAGMA wal_checkpoint(FULL);\nSELECT text FROM message ORDER BY date DESC LIMIT 1;": {
				ExitCode: 0, Stdout: "0|3|3\nmeeting at noon\n",
			},
		},
	}

	sqliteCheck := Check{
		Func: StringList{"sqlite_row_matches"},
		Result: GetterSpec{
			Kind:  "sqlite",
			Path:  "/Users/tmc/Library/Messages/chat.db",
			Query: "SELECT text FROM message ORDER BY date DESC LIMIT 1;",
		},
		Options: map[string]any{"expected": "meeting at noon"},
	}
	imageCheck := Check{
		Func:    StringList{"image_similar"},
		Result:  GetterSpec{Kind: "file", Path: "/Users/tmc/Desktop/out.png"},
		Options: map[string]any{"expected": whiteImg},
	}

	tests := []struct {
		name string
		comp Composite
		want float64
	}{
		{
			name: "and both pass",
			comp: Composite{Conj: "and", Checks: []Check{sqliteCheck, imageCheck}},
			want: 1,
		},
		{
			name: "and one fails -> partial",
			comp: Composite{Conj: "and", Checks: []Check{
				{Func: StringList{"sqlite_row_matches"}, Result: sqliteCheck.Result, Options: map[string]any{"expected": "wrong text"}},
				imageCheck,
			}},
			want: 0.5,
		},
		{
			name: "or one passes",
			comp: Composite{Conj: "or", Checks: []Check{
				{Func: StringList{"sqlite_row_matches"}, Result: sqliteCheck.Result, Options: map[string]any{"expected": "wrong text"}},
				imageCheck,
			}},
			want: 1,
		},
		{
			name: "single check no conj",
			comp: Composite{Checks: []Check{imageCheck}},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.comp.Evaluate(probe, nil)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompositeExpectedGetter(t *testing.T) {
	// A check may read its gold from a second getter (Expected), not just a
	// literal option: here both Result and Expected read files off the guest.
	whiteImg := compositeWhitePNG(t)
	probe := FakeProbe{Files: map[string]string{
		"/got.png":  whiteImg,
		"/gold.png": whiteImg,
	}}
	comp := Composite{Checks: []Check{{
		Func:     StringList{"image_similar"},
		Result:   GetterSpec{Kind: "file", Path: "/got.png"},
		Expected: &GetterSpec{Kind: "file", Path: "/gold.png"},
	}}}
	got, err := comp.Evaluate(probe, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got != 1 {
		t.Fatalf("score = %v, want 1", got)
	}
}

func TestCompositeValidate(t *testing.T) {
	goodGetter := GetterSpec{Kind: "file", Path: "/x"}
	tests := []struct {
		name string
		comp Composite
		ok   bool
	}{
		{"empty checks", Composite{}, false},
		{"single ok", Composite{Checks: []Check{{Func: StringList{"file_exists"}, Result: goodGetter}}}, true},
		{"list needs conj", Composite{Checks: []Check{
			{Func: StringList{"file_exists"}, Result: goodGetter},
			{Func: StringList{"file_exists"}, Result: goodGetter},
		}}, false},
		{"list bad conj", Composite{Conj: "xor", Checks: []Check{
			{Func: StringList{"file_exists"}, Result: goodGetter},
			{Func: StringList{"file_exists"}, Result: goodGetter},
		}}, false},
		{"list good conj", Composite{Conj: "and", Checks: []Check{
			{Func: StringList{"file_exists"}, Result: goodGetter},
			{Func: StringList{"file_exists"}, Result: goodGetter},
		}}, true},
		{"check empty func", Composite{Checks: []Check{{Result: goodGetter}}}, false},
		{"check unknown metric", Composite{Checks: []Check{{Func: StringList{"telepathy"}, Result: goodGetter}}}, false},
		{"check bad getter", Composite{Checks: []Check{{Func: StringList{"file_exists"}, Result: GetterSpec{Kind: "file"}}}}, false},
		{"check metric-list needs conj", Composite{Checks: []Check{
			{Func: StringList{"file_exists", "exact_match"}, Result: goodGetter},
		}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.comp.validate()
			if tt.ok && err != nil {
				t.Fatalf("validate = %v, want nil", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("validate = nil, want error")
			}
		})
	}
}

func TestCompositeTier(t *testing.T) {
	// The composite's tier is the max over all check getters: a TierA file check
	// plus a TierB sqlite check is TierB.
	comp := Composite{Conj: "and", Checks: []Check{
		{Func: StringList{"file_exists"}, Result: GetterSpec{Kind: "file", Path: "/x"}},
		{Func: StringList{"sqlite_row_matches"}, Result: GetterSpec{Kind: "sqlite", Path: "/db", Query: "SELECT 1"}},
	}}
	if got := comp.Tier(); got != TierB {
		t.Fatalf("Tier = %v, want %v", got, TierB)
	}
}

func TestCompositeGetterError(t *testing.T) {
	// A getter failure (missing file) surfaces as an error, never a silent 0.
	comp := Composite{Checks: []Check{{
		Func:   StringList{"file_exists"},
		Result: GetterSpec{Kind: "file", Path: "/missing"},
	}}}
	if _, err := comp.Evaluate(FakeProbe{}, nil); err == nil {
		t.Fatal("want error from missing-file getter, got nil")
	}
}
