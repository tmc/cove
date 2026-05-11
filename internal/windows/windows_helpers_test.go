package windows

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPowerShellPathHelpers(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantParent string
		wantQuote  string
	}{
		{name: "no parent", path: `marker`, wantParent: ".", wantQuote: `marker`},
		{name: "normal path", path: `C:\ProgramData\cove\provisioned`, wantParent: `C:\ProgramData\cove`, wantQuote: `C:\ProgramData\cove\provisioned`},
		{name: "single quote", path: `C:\Users\O'Brien\marker`, wantParent: `C:\Users\O''Brien`, wantQuote: `C:\Users\O''Brien\marker`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := psParent(tt.path); got != tt.wantParent {
				t.Fatalf("psParent(%q) = %q, want %q", tt.path, got, tt.wantParent)
			}
			if got := psSingleQuote(tt.path); got != tt.wantQuote {
				t.Fatalf("psSingleQuote(%q) = %q, want %q", tt.path, got, tt.wantQuote)
			}
		})
	}
}

func TestGenerateAutounattendXMLMarkerWithoutParent(t *testing.T) {
	xml := GenerateAutounattendXML(ProvisionConfig{
		Username:   "alice",
		Password:   "secret",
		Hostname:   "winhost",
		LocalAdmin: true,
		MarkerPath: "provisioned",
	})
	if !strings.Contains(xml, `New-Item -ItemType Directory -Force &#39;.&#39;`) {
		t.Fatalf("autounattend.xml missing current-directory marker parent:\n%s", xml)
	}
}

func TestDefaultVirtIODriversCacheDirUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := DefaultVirtIODriversCacheDir()
	if err != nil {
		t.Fatalf("DefaultVirtIODriversCacheDir: %v", err)
	}
	want := filepath.Join(home, ".vz", "windows-drivers")
	if got != want {
		t.Fatalf("DefaultVirtIODriversCacheDir() = %q, want %q", got, want)
	}
}

func TestEnsureVirtIODriversISOWrapsCacheDirError(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "not-dir")
	if err := os.WriteFile(cacheDir, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := EnsureVirtIODriversISO(cacheDir)
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("EnsureVirtIODriversISO() error = %v, want ENOTDIR", err)
	}
}
