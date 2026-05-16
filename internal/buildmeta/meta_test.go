package buildmeta

import (
	"testing"
	"time"
)

func TestParseScript(t *testing.T) {
	data := []byte(`# cache-env: PATH, HOME
# cache-file: ~/go.mod
# cache-url: https://example.test/a
# cache-ttl: 7d
# secret: TOKEN
# secret-from: OUT=env://TOKEN
# compact: thorough

exec echo ok
`)
	got, err := ParseScript(data)
	if err != nil {
		t.Fatalf("ParseScript: %v", err)
	}
	if got.CacheTTL != 7*24*time.Hour || got.Compact != "thorough" {
		t.Fatalf("ParseScript = %#v", got)
	}
	if len(got.SecretFrom) != 1 || got.SecretFrom[0].Name != "OUT" || got.SecretFrom[0].URI != "env://TOKEN" {
		t.Fatalf("SecretFrom = %#v", got.SecretFrom)
	}
}

func TestParseSecretFromRejectsSlashName(t *testing.T) {
	_, err := ParseSecretFrom("bad/name=env://TOKEN", 3)
	if err == nil {
		t.Fatal("ParseSecretFrom succeeded, want error")
	}
}

func TestParseDuration(t *testing.T) {
	got, err := ParseDuration("2h")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got != 2*time.Hour {
		t.Fatalf("ParseDuration = %v, want 2h", got)
	}
}

func TestValidateCompactMode(t *testing.T) {
	if err := ValidateCompactMode("targeted"); err != nil {
		t.Fatalf("ValidateCompactMode: %v", err)
	}
	if err := ValidateCompactMode("wide"); err == nil {
		t.Fatal("ValidateCompactMode(wide) = nil, want error")
	}
}
