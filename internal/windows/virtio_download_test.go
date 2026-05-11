package windows

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadVirtIODriversISO(t *testing.T) {
	large := strings.Repeat("x", minVirtIODriversSize)
	tests := []struct {
		name       string
		body       string
		status     int
		dest       func(string) string
		wantErr    string
		wantIs     error
		wantCached bool
	}{
		{
			name:       "success",
			body:       large,
			status:     http.StatusOK,
			dest:       func(dir string) string { return filepath.Join(dir, virtIODriversISOName) },
			wantCached: true,
		},
		{
			name:    "too small removes partial",
			body:    "small",
			status:  http.StatusOK,
			dest:    func(dir string) string { return filepath.Join(dir, virtIODriversISOName) },
			wantErr: "virtio iso too small",
		},
		{
			name:    "missing parent wraps",
			body:    large,
			status:  http.StatusOK,
			dest:    func(dir string) string { return filepath.Join(dir, "missing", virtIODriversISOName) },
			wantErr: "create virtio iso",
			wantIs:  os.ErrNotExist,
		},
		{
			name:    "http error",
			status:  http.StatusTeapot,
			dest:    func(dir string) string { return filepath.Join(dir, virtIODriversISOName) },
			wantErr: "418",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			oldURL := virtIODriversURL
			virtIODriversURL = server.URL
			t.Cleanup(func() { virtIODriversURL = oldURL })

			dir := t.TempDir()
			dest := tt.dest(dir)
			err := downloadVirtIODriversISO(dest)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("downloadVirtIODriversISO() error = %v, want %q", err, tt.wantErr)
				}
				if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
					t.Fatalf("downloadVirtIODriversISO() error = %v, want Is %v", err, tt.wantIs)
				}
				if _, statErr := os.Stat(dest + ".tmp"); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("partial tmp exists after failure: %v", statErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("downloadVirtIODriversISO(): %v", err)
			}
			info, err := os.Stat(dest)
			if err != nil {
				t.Fatalf("stat cached iso: %v", err)
			}
			if tt.wantCached && info.Size() != int64(len(tt.body)) {
				t.Fatalf("cached size = %d, want %d", info.Size(), len(tt.body))
			}
		})
	}
}
