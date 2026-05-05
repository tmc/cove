package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	t.Setenv("COVE_SECRET_TOKEN", "alpha")
	t.Setenv("COVE_SECRET_EMPTY", "")
	dir := t.TempDir()
	file0600 := filepath.Join(dir, "secret-0600")
	file0400 := filepath.Join(dir, "secret-0400")
	file0644 := filepath.Join(dir, "secret-0644")
	if err := os.WriteFile(file0600, []byte("file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file0400, []byte("read-only"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file0644, []byte("open"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		uri     string
		want    string
		wantErr string
	}{
		{name: "env set", uri: "env://COVE_SECRET_TOKEN", want: "alpha"},
		{name: "env empty", uri: "env://COVE_SECRET_EMPTY", want: ""},
		{name: "env missing", uri: "env://COVE_SECRET_MISSING", wantErr: "secret env://COVE_SECRET_MISSING not set"},
		{name: "env empty name", uri: "env://", wantErr: "secret env://: empty environment variable name"},
		{name: "file 0600", uri: "file://" + file0600, want: "file\n"},
		{name: "file 0400", uri: "file://" + file0400, want: "read-only"},
		{name: "file too open", uri: "file://" + file0644, wantErr: "permissions 0644 too open; require 0600 or stricter"},
		{name: "file directory", uri: "file://" + dir, wantErr: "is a directory"},
		{name: "file hostless relative", uri: "file:relative", wantErr: "file URI must use an absolute path"},
		{name: "file host relative", uri: "file://relative", wantErr: "file URI must use an absolute path"},
		{name: "unsupported scheme", uri: "1password://vault/item/field", wantErr: `unsupported secret URI scheme "1password" (supported: env, file)`},
		{name: "missing scheme", uri: "TOKEN", wantErr: `secret URI "TOKEN": missing scheme`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.uri)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Resolve(%q) = %q, %v; want error containing %q", tt.uri, got, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.uri, err)
			}
			if string(got) != tt.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestResolveReturnsCallerOwnedBytes(t *testing.T) {
	t.Setenv("COVE_SECRET_TOKEN", "alpha")
	first, err := Resolve("env://COVE_SECRET_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'X'
	second, err := Resolve("env://COVE_SECRET_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != "alpha" {
		t.Fatalf("second Resolve = %q, want alpha", second)
	}
}
