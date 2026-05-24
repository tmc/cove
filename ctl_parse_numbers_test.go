package main

import "testing"

func TestParseNonNegativeInt(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{"zero", "0", 0, false},
		{"positive", "42", 42, false},
		{"negative", "-1", 0, true},
		{"not a number", "abc", 0, true},
		{"empty", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseNonNegativeInt(tt.value, "count")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}

func TestParseOptionalNonNegativeInt(t *testing.T) {
	if got, err := parseOptionalNonNegativeInt(nil, "x"); err != nil || got != 0 {
		t.Fatalf("nil: got %d err %v", got, err)
	}
	if got, err := parseOptionalNonNegativeInt([]string{"7"}, "x"); err != nil || got != 7 {
		t.Fatalf("single: got %d err %v", got, err)
	}
	if _, err := parseOptionalNonNegativeInt([]string{"1", "2"}, "x"); err == nil {
		t.Fatal("too many: want error")
	}
	if _, err := parseOptionalNonNegativeInt([]string{"-3"}, "x"); err == nil {
		t.Fatal("negative: want error")
	}
}

func TestParseUint32(t *testing.T) {
	if got, err := parseUint32("123", "n"); err != nil || got != 123 {
		t.Fatalf("decimal: got %d err %v", got, err)
	}
	if got, err := parseUint32("0x10", "n"); err != nil || got != 16 {
		t.Fatalf("hex: got %d err %v", got, err)
	}
	if _, err := parseUint32("-1", "n"); err == nil {
		t.Fatal("negative: want error")
	}
	if _, err := parseUint32("99999999999", "n"); err == nil {
		t.Fatal("overflow: want error")
	}
}
