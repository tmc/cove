package anthropicadapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIntNumber(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int
	}{
		{"int", int(7), 7},
		{"int64", int64(42), 42},
		{"float64 truncates", 3.9, 3},
		{"json.Number", json.Number("123"), 123},
		{"json.Number invalid yields 0", json.Number("nope"), 0},
		{"unsupported string", "5", 0},
		{"nil", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := intNumber(tt.in); got != tt.want {
				t.Errorf("intNumber(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestCoordinates(t *testing.T) {
	tests := []struct {
		name    string
		input   map[string]any
		x, y    int
		wantErr bool
	}{
		{"coordinate slice", map[string]any{"coordinate": []any{float64(11), float64(22)}}, 11, 22, false},
		{"x/y fallback", map[string]any{"x": int(3), "y": int64(4)}, 3, 4, false},
		{"missing keys default zero", map[string]any{}, 0, 0, false},
		{"coordinate wrong length", map[string]any{"coordinate": []any{float64(1)}}, 0, 0, true},
		{"coordinate wrong type", map[string]any{"coordinate": "not a slice"}, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, y, err := coordinates(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("coordinates err = %v, wantErr=%v", err, tt.wantErr)
			}
			if err == nil {
				if x != tt.x || y != tt.y {
					t.Errorf("coordinates = (%d,%d), want (%d,%d)", x, y, tt.x, tt.y)
				}
			}
		})
	}
}

func TestKeyCode(t *testing.T) {
	const (
		shift = uint(1) << 17
		ctrl  = uint(1) << 18
		alt   = uint(1) << 19
		cmd   = uint(1) << 20
	)
	tests := []struct {
		name     string
		input    map[string]any
		wantCode uint16
		wantMods uint
		wantErr  string
	}{
		{"plain letter", map[string]any{"key": "a"}, 0, 0, ""},
		{"named key", map[string]any{"key": "Return"}, 36, 0, ""},
		{"falls back to text", map[string]any{"text": "tab"}, 48, 0, ""},
		{"shift modifier", map[string]any{"key": "shift+a"}, 0, shift, ""},
		{"cmd alias", map[string]any{"key": "cmd+c"}, 8, cmd, ""},
		{"meta alias", map[string]any{"key": "meta+v"}, 9, cmd, ""},
		{"option alias", map[string]any{"key": "option+x"}, 7, alt, ""},
		{"ctrl alias", map[string]any{"key": "control+z"}, 6, ctrl, ""},
		{"unsupported", map[string]any{"key": "f99"}, 0, 0, "unsupported key"},
		{"empty input errors", map[string]any{}, 0, 0, "unsupported key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, mods, err := keyCode(tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if code != tt.wantCode || mods != tt.wantMods {
				t.Errorf("keyCode(%v) = (%d, %d), want (%d, %d)", tt.input, code, mods, tt.wantCode, tt.wantMods)
			}
		})
	}
}
