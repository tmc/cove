package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseIPSWSource(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		isURL bool
		path  string
		err   bool
	}{
		{"path", "/tmp/Restore.ipsw", false, "/tmp/Restore.ipsw", false},
		{"trimmed path", "  ./Restore.ipsw\n", false, "./Restore.ipsw", false},
		{"http url", "http://example.test/Restore.ipsw", true, "", false},
		{"https url", "https://example.test/Restore.ipsw", true, "", false},
		{"file url", "file:///tmp/Restore.ipsw", false, "/tmp/Restore.ipsw", false},
		{"empty", "", false, "", true},
		{"bad scheme", "ftp://example.test/Restore.ipsw", false, "", true},
		{"missing host", "https:///Restore.ipsw", false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIPSWSource(tt.in)
			if tt.err {
				if err == nil {
					t.Fatalf("parseIPSWSource(%q) = nil error, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIPSWSource(%q): %v", tt.in, err)
			}
			if got.IsURL != tt.isURL || got.Path != tt.path {
				t.Fatalf("source = %+v, want isURL=%v path=%q", got, tt.isURL, tt.path)
			}
		})
	}
}

func TestVerifyIPSWFileSentinels(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.ipsw")
	small := filepath.Join(dir, "small.ipsw")
	corrupt := filepath.Join(dir, "corrupt.ipsw")
	good := filepath.Join(dir, "good.ipsw")
	if err := os.WriteFile(small, []byte("PK\x05\x06"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeSparseIPSW(t, corrupt, false)
	makeSparseIPSW(t, good, true)

	tests := []struct {
		name string
		path string
		want error
	}{
		{"missing", missing, ErrIPSWMissing},
		{"small", small, ErrIPSWTooSmall},
		{"corrupt", corrupt, ErrIPSWCorrupt},
		{"good", good, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyIPSWFile(tt.path)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("verifyIPSWFile: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDownloadIPSWCurlTooSmallFromHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "4")
			return
		}
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	err := downloadIPSWCurl(srv.URL+"/Restore.ipsw", filepath.Join(t.TempDir(), "Restore.ipsw"))
	if !errors.Is(err, ErrIPSWTooSmall) {
		t.Fatalf("err = %v, want ErrIPSWTooSmall", err)
	}
}

func makeSparseIPSW(t *testing.T, path string, eocd bool) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	size := int64(ipswMinSize) + 4096
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if eocd {
		if _, err := f.WriteAt([]byte{0x50, 0x4b, 0x05, 0x06}, size-22); err != nil {
			t.Fatal(err)
		}
	}
}
