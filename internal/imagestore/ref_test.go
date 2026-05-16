package imagestore

import (
	"errors"
	"testing"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{in: "base", want: Ref{Name: "base", Tag: "latest"}},
		{in: "base:v1", want: Ref{Name: "base", Tag: "v1"}},
		{in: "agentkit/linux-base:latest", want: Ref{Name: "agentkit/linux-base", Tag: "latest"}},
		{in: "", wantErr: true},
		{in: ":v1", wantErr: true},
		{in: "base:", wantErr: true},
		{in: "a:b:c", wantErr: true},
		{in: "../x:v1", wantErr: true},
		{in: "registry.example.com/repo:v1", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseRef(tt.in)
		if tt.wantErr {
			if !errors.Is(err, ErrRefInvalid) {
				t.Fatalf("ParseRef(%q) error = %v, want ErrRefInvalid", tt.in, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseRef(%q) = %#v, want %#v", tt.in, got, tt.want)
		}
		if got.String() != got.Name+":"+got.Tag {
			t.Fatalf("String() = %q", got.String())
		}
	}
}
