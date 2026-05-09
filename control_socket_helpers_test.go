package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOCRData(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantText  string
		wantTimeo string
		wantReg   string
	}{
		{name: "empty", raw: ``, wantText: ""},
		{name: "no data field", raw: `{"type":"ocr-click"}`, wantText: ""},
		{name: "full", raw: `{"type":"ocr-click","data":{"text":"OK","timeout":"5s","region":"top"}}`,
			wantText: "OK", wantTimeo: "5s", wantReg: "top"},
		{name: "partial", raw: `{"data":{"text":"hi"}}`, wantText: "hi"},
		{name: "malformed top", raw: `not json`, wantText: ""},
		{name: "malformed data", raw: `{"data":"oops"}`, wantText: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOCRData([]byte(tt.raw))
			if got.Text != tt.wantText || got.Timeout != tt.wantTimeo || got.Region != tt.wantReg {
				t.Errorf("parseOCRData(%q) = %+v", tt.raw, got)
			}
		})
	}
}

func TestGetControlSocketPathForVM(t *testing.T) {
	got := GetControlSocketPathForVM("/some/vm")
	if got != filepath.Join("/some/vm", "control.sock") {
		t.Errorf("got %q", got)
	}
	if !strings.HasSuffix(GetControlTokenPathForVM("/some/vm"), controlTokenFileName) {
		t.Errorf("token path missing suffix: %q", GetControlTokenPathForVM("/some/vm"))
	}
}

func TestLoadControlTokenFromPath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")
	if _, err := LoadControlTokenFromPath(missing); err == nil {
		t.Fatal("want error for missing file")
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("   \n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadControlTokenFromPath(empty); err == nil {
		t.Fatal("want error for empty token")
	}

	good := filepath.Join(dir, "tok")
	if err := os.WriteFile(good, []byte("abc123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadControlTokenFromPath(good)
	if err != nil || tok != "abc123" {
		t.Fatalf("got (%q,%v)", tok, err)
	}
}

func TestEnsureControlTokenForVM(t *testing.T) {
	t.Run("from env", func(t *testing.T) {
		dir := t.TempDir()
		vmDir := filepath.Join(dir, "vm")
		t.Setenv(controlTokenEnvVar, "envtoken")
		tok, err := EnsureControlTokenForVM(vmDir)
		if err != nil || tok != "envtoken" {
			t.Fatalf("got (%q,%v)", tok, err)
		}
		data, _ := os.ReadFile(filepath.Join(vmDir, controlTokenFileName))
		if strings.TrimSpace(string(data)) != "envtoken" {
			t.Errorf("file content %q", data)
		}
	})
	t.Run("generate", func(t *testing.T) {
		dir := t.TempDir()
		vmDir := filepath.Join(dir, "vm")
		t.Setenv(controlTokenEnvVar, "")
		tok, err := EnsureControlTokenForVM(vmDir)
		if err != nil || len(tok) != 64 {
			t.Fatalf("got (%q len=%d, %v)", tok, len(tok), err)
		}
		// idempotent: second call returns same token
		tok2, err := EnsureControlTokenForVM(vmDir)
		if err != nil || tok2 != tok {
			t.Fatalf("not idempotent: %q vs %q (%v)", tok, tok2, err)
		}
	})
}

func TestEffectiveVMDir(t *testing.T) {
	s := &ControlServer{vmDir: "/explicit"}
	if got := s.effectiveVMDir(); got != "/explicit" {
		t.Errorf("explicit got %q", got)
	}
	s2 := &ControlServer{}
	if got := s2.effectiveVMDir(); got != vmDir {
		t.Errorf("default got %q want %q", got, vmDir)
	}
}
