package main

import (
	"archive/tar"
	"strings"
	"testing"
)

func TestCheckTarEntryRejections(t *testing.T) {
	tests := []struct {
		name    string
		hdr     *tar.Header
		wantSub string
	}{
		{"nil", nil, "nil tar header"},
		{"symlink", &tar.Header{Name: "link", Typeflag: tar.TypeSymlink}, "disallowed typeflag"},
		{"empty", &tar.Header{Name: "", Typeflag: tar.TypeReg}, "empty tar entry name"},
		{"absolute", &tar.Header{Name: "/abs", Typeflag: tar.TypeReg}, "absolute path"},
		{"separator", &tar.Header{Name: "sub/x", Typeflag: tar.TypeReg}, "path separator"},
		{"dotdot", &tar.Header{Name: "..", Typeflag: tar.TypeReg}, "unsafe"},
		{"linkname", &tar.Header{Name: "ok", Typeflag: tar.TypeReg, Linkname: "x"}, "linkname"},
		{"negativeSize", &tar.Header{Name: "ok", Typeflag: tar.TypeReg, Size: -1}, "negative size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkTarEntry(tt.hdr)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want %q", err, tt.wantSub)
			}
		})
	}
}
