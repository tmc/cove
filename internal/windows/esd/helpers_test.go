package esd

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProgressReader(t *testing.T) {
	var buf bytes.Buffer
	src := strings.NewReader("0123456789")
	pr := &progressReader{
		r:      src,
		total:  10,
		output: &buf,
	}
	out := make([]byte, 4)
	n, err := pr.Read(out)
	if err != nil {
		t.Fatalf("first Read err = %v", err)
	}
	if n != 4 || pr.done != 4 {
		t.Errorf("first Read: n=%d done=%d, want n=4 done=4", n, pr.done)
	}
	if !strings.Contains(buf.String(), "%") {
		t.Errorf("first Read should print percent line, got %q", buf.String())
	}

	// Second Read happens within the same second; suppressed unless EOF.
	buf.Reset()
	out = make([]byte, 4)
	n, err = pr.Read(out)
	if err != nil {
		t.Fatalf("mid Read err = %v", err)
	}
	if n != 4 || pr.done != 8 {
		t.Errorf("mid Read: n=%d done=%d, want n=4 done=8", n, pr.done)
	}
	if buf.Len() != 0 {
		t.Errorf("mid Read should not emit progress within 1s, got %q", buf.String())
	}

	// Force the >=1s elapsed branch by backdating last; this Read prints again.
	buf.Reset()
	pr.last = time.Now().Add(-2 * time.Second)
	out = make([]byte, 4)
	n, _ = pr.Read(out)
	if n != 2 || pr.done != 10 {
		t.Errorf("final Read: n=%d done=%d, want n=2 done=10", n, pr.done)
	}
	if !strings.Contains(buf.String(), "%") {
		t.Errorf("elapsed Read must emit progress, got %q", buf.String())
	}
}

func TestProgressReaderUnknownTotal(t *testing.T) {
	var buf bytes.Buffer
	pr := &progressReader{
		r:      strings.NewReader("xyz"),
		output: &buf,
		last:   time.Now().Add(-2 * time.Second),
	}
	if _, err := pr.Read(make([]byte, 3)); err != nil {
		t.Fatalf("Read err = %v", err)
	}
	if !strings.Contains(buf.String(), "GB") || strings.Contains(buf.String(), "%") {
		t.Errorf("zero-total format should be 'X.X GB' without percent, got %q", buf.String())
	}
}

func TestOptionsClient(t *testing.T) {
	custom := &http.Client{Timeout: time.Second}
	if got := (Options{Client: custom}).client(); got != custom {
		t.Errorf("Options.client() with custom = %v, want %v", got, custom)
	}
	if got := (Options{}).client(); got != http.DefaultClient {
		t.Errorf("Options.client() default = %v, want http.DefaultClient", got)
	}
}

func TestOptionsWithDefaultsPreservesSetValues(t *testing.T) {
	in := Options{
		CatalogURL:   "https://example.test/catalog",
		LanguageCode: "fr-fr",
		Architecture: "x64",
		TarPath:      "/usr/local/bin/tar",
	}
	got := in.withDefaults()
	if got.CatalogURL != in.CatalogURL || got.LanguageCode != in.LanguageCode || got.Architecture != in.Architecture || got.TarPath != in.TarPath {
		t.Errorf("withDefaults overwrote set values: %#v", got)
	}
}

func TestEntryRank(t *testing.T) {
	tests := []struct {
		name    string
		entry   Entry
		wantInt int
	}{
		{"consumer in name", Entry{Name: "ClientConsumer.esd"}, 0},
		{"consumer in edition", Entry{Edition: "CLIENTCONSUMER"}, 0},
		{"business in name", Entry{Name: "ClientBusiness.esd"}, 1},
		{"business in edition", Entry{Edition: "ClientBusiness"}, 1},
		{"fallthrough", Entry{Name: "other.esd", Edition: "Server"}, 2},
		{"empty entry", Entry{}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := entryRank(tt.entry); got != tt.wantInt {
				t.Errorf("entryRank(%+v) = %d, want %d", tt.entry, got, tt.wantInt)
			}
		})
	}
}
