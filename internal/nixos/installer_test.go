package nixos

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestISOConstants(t *testing.T) {
	if !strings.Contains(ISOURL, "nixos-25.11") {
		t.Fatalf("ISOURL = %q, want 25.11 channel", ISOURL)
	}
	if !strings.Contains(ISOURL, "minimal-aarch64-linux.iso") {
		t.Fatalf("ISOURL = %q, want minimal aarch64 iso", ISOURL)
	}
	if got := CachedISOPath("/tmp/cache"); got != filepath.Join("/tmp/cache", ISOName) {
		t.Fatalf("CachedISOPath = %q", got)
	}
}

func TestCheckISOURL(t *testing.T) {
	if os.Getenv("COVE_NETWORK_TESTS") == "" {
		t.Skip("set COVE_NETWORK_TESTS=1 to check nixos iso URL")
	}
	if err := CheckISOURL(&http.Client{}); err != nil {
		t.Fatal(err)
	}
}
