package vmpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateRejectsNegative(t *testing.T) {
	cases := []Policy{
		{IdleTimeout: -1},
		{MaxAge: -time.Second},
		{RunBudget: -1},
	}
	for _, p := range cases {
		if err := p.Validate(); err == nil {
			t.Errorf("Validate(%#v) = nil, want error", p)
		}
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	if err := Save(t.TempDir(), Policy{RunBudget: -1}); err == nil {
		t.Fatal("Save(negative budget) = nil, want error")
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "parse vm policy") {
		t.Fatalf("Load(malformed) err = %v, want parse error", err)
	}
}

func TestLoadBadDuration(t *testing.T) {
	dir := t.TempDir()
	for _, body := range []string{`{"idleTimeout":"twelve"}`, `{"maxAge":"forever"}`} {
		if err := os.WriteFile(Path(dir), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Fatalf("Load(%s) = nil, want error", body)
		}
	}
}

func TestLoadNegativeRunBudgetOnDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte(`{"runBudget":-5}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load(negative budget) = nil, want error")
	}
}

func TestClearIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := Clear(dir); err != nil {
		t.Fatalf("Clear(missing) = %v, want nil", err)
	}
	if err := Save(dir, Policy{RunBudget: 5}); err != nil {
		t.Fatal(err)
	}
	if err := Clear(dir); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); !os.IsNotExist(err) {
		t.Fatalf("policy file still exists after Clear, stat err = %v", err)
	}
}

func TestParseRunBudgetRejectsNonNumeric(t *testing.T) {
	for _, raw := range []string{"twelve", "-3"} {
		if _, err := ParseRunBudget(raw); err == nil {
			t.Errorf("ParseRunBudget(%q) = nil, want error", raw)
		}
	}
}
