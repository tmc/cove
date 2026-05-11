package ociimage

import (
	"strings"
	"testing"
)

func TestParseReference(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want Reference
	}{
		{
			name: "tagged ghcr ref",
			ref:  "ghcr.io/acme/macos-15:latest",
			want: Reference{
				Registry:   "ghcr.io",
				Repository: "acme/macos-15",
				Tag:        "latest",
			},
		},
		{
			name: "localhost port",
			ref:  "localhost:5000/team/dev.vm:v1.2.3",
			want: Reference{
				Registry:   "localhost:5000",
				Repository: "team/dev.vm",
				Tag:        "v1.2.3",
			},
		},
		{
			name: "digest",
			ref:  "registry.example.com/team/vm@sha256:abcd",
			want: Reference{
				Registry:   "registry.example.com",
				Repository: "team/vm",
				Digest:     "sha256:abcd",
			},
		},
		{
			name: "tag and digest",
			ref:  "registry.example.com/team/vm:base@sha256:abcd",
			want: Reference{
				Registry:   "registry.example.com",
				Repository: "team/vm",
				Tag:        "base",
				Digest:     "sha256:abcd",
			},
		},
		{
			name: "docker transport prefix",
			ref:  "docker://ghcr.io/trycua/ubuntu-noble-vanilla:latest",
			want: Reference{
				Registry:   "ghcr.io",
				Repository: "trycua/ubuntu-noble-vanilla",
				Tag:        "latest",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReference(tt.ref)
			if err != nil {
				t.Fatalf("ParseReference() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseReference() = %#v, want %#v", got, tt.want)
			}
			if !strings.HasPrefix(tt.ref, "docker://") && got.String() != tt.ref {
				t.Fatalf("Reference.String() = %q, want %q", got.String(), tt.ref)
			}
		})
	}
}

func TestParseReferenceRejectsInvalid(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		wantErr string
	}{
		{name: "empty", ref: "", wantErr: "empty reference"},
		{name: "scheme", ref: "https://ghcr.io/acme/vm:latest", wantErr: "URL scheme"},
		{name: "missing registry", ref: "acme/vm", wantErr: "registry and repository"},
		{name: "missing repo", ref: "ghcr.io/:latest", wantErr: "registry and repository"},
		{name: "registry leading dot", ref: ".example.com/acme/vm:latest", wantErr: "invalid registry"},
		{name: "uppercase repo", ref: "ghcr.io/Acme/vm:latest", wantErr: "invalid repository"},
		{name: "empty repo segment", ref: "ghcr.io/acme//vm:latest", wantErr: "invalid repository"},
		{name: "bad registry port", ref: "localhost:http/acme/vm:latest", wantErr: "invalid registry"},
		{name: "bad tag", ref: "ghcr.io/acme/vm:-bad", wantErr: "invalid tag"},
		{name: "empty digest", ref: "ghcr.io/acme/vm@", wantErr: "empty digest"},
		{name: "multiple digests", ref: "ghcr.io/acme/vm@sha256:abcd@sha256:ef", wantErr: "invalid digest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseReference(tt.ref)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ParseReference() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTag(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		wantErr bool
	}{
		{name: "simple", tag: "latest"},
		{name: "version", tag: "v1.2_3-alpha"},
		{name: "empty", tag: "", wantErr: true},
		{name: "leading dot", tag: ".bad", wantErr: true},
		{name: "slash", tag: "team/latest", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTag(tt.tag)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateTag() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
