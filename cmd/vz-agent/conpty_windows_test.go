package main

import (
	"reflect"
	"testing"
)

func TestConPTYSize(t *testing.T) {
	for _, tt := range []struct {
		name       string
		rows, cols uint32
		valid      bool
	}{
		{"normal", 24, 80, true}, {"maximum", 32767, 32767, true}, {"zero rows", 0, 80, false}, {"zero columns", 24, 0, false}, {"overflow rows", 32768, 80, false}, {"overflow columns", 24, 32768, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			size, err := conPTYSize(tt.rows, tt.cols)
			if (err == nil) != tt.valid {
				t.Fatalf("size error = %v, valid = %v", err, tt.valid)
			}
			if tt.valid && (uint32(size.X) != tt.cols || uint32(size.Y) != tt.rows) {
				t.Fatalf("size = %v", size)
			}
		})
	}
}

func TestConPTYEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name    string
		input   []string
		want    []uint16
		wantErr bool
	}{
		{"empty", nil, []uint16{0, 0}, false},
		{"sorted", []string{"z=2", "A=1"}, []uint16{'A', '=', '1', 0, 'z', '=', '2', 0, 0}, false},
		{"unicode", []string{"A=😀"}, []uint16{'A', '=', 0xd83d, 0xde00, 0, 0}, false},
		{"nul", []string{"A=x\x00y"}, nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]string(nil), tt.input...)
			got, err := conPTYEnvironment(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("environment = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(before, tt.input) {
				t.Fatal("input mutated")
			}
		})
	}
}
