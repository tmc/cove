package metrics

import (
	"strings"
	"sync"
	"testing"
)

func TestMaskerApply(t *testing.T) {
	tests := []struct {
		name    string
		secrets []string
		in      string
		want    string
	}{
		{"empty masker", nil, "hello world", "hello world"},
		{"no match", []string{"sekret"}, "hello world", "hello world"},
		{"single match", []string{"sekret"}, "token=sekret\n", "token=***\n"},
		{"multiple matches", []string{"abc"}, "abc and abc again", "*** and *** again"},
		{"longest first", []string{"abc", "abcdef"}, "abcdef abc", "*** ***"},
		{"overlapping prefers longer", []string{"foo", "foobar"}, "foobar foo foox", "*** *** ***x"},
		{"ignores 1-byte", []string{"x"}, "x marks the spot", "x marks the spot"},
		{"ignores empty", []string{""}, "anything", "anything"},
		{"binary safe", []string{"\x00\x01secret"}, "lead\x00\x01secrettail", "lead***tail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMasker()
			for _, s := range tt.secrets {
				m.AddString(s)
			}
			if got := m.ApplyString(tt.in); got != tt.want {
				t.Errorf("Apply(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMaskerApplyDoesNotMutateInput(t *testing.T) {
	m := NewMasker()
	m.AddString("secret")
	in := []byte("token=secret")
	original := string(in)
	_ = m.Apply(in)
	if string(in) != original {
		t.Errorf("Apply mutated input: got %q, want %q", in, original)
	}
}

func TestMaskerNilSafe(t *testing.T) {
	var m *Masker
	if got := m.ApplyString("hi"); got != "hi" {
		t.Errorf("nil ApplyString = %q, want %q", got, "hi")
	}
	m.AddString("ignored") // must not panic
}

func TestMaskerDedup(t *testing.T) {
	m := NewMasker()
	m.AddString("abc")
	m.AddString("abc")
	if got := m.ApplyString("abc abc"); !strings.Contains(got, "***") {
		t.Errorf("expected mask, got %q", got)
	}
}

func TestMaskerConcurrent(t *testing.T) {
	m := NewMasker()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.AddString(strings.Repeat("s", i+2))
			_ = m.ApplyString("ssssssssssssss")
		}(i)
	}
	wg.Wait()
}
