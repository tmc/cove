package main

import (
	"reflect"
	"testing"
)

func TestDockerRegistryKeys(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		want     []string
	}{
		{"empty", "", nil},
		{"whitespaceOnly", "   ", nil},
		{"trailingSlash", "ghcr.io/", []string{"ghcr.io", "https://ghcr.io", "http://ghcr.io"}},
		{"plain", "registry.example.com", []string{"registry.example.com", "https://registry.example.com", "http://registry.example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dockerRegistryKeys(tt.registry)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got = %v, want %v", got, tt.want)
			}
		})
	}
}
