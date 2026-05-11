package esd

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseProductsXML(t *testing.T) {
	entries, err := ParseProductsXML(stringsReader(productsXML("http://example.test/win.esd", "arm64", "en-us", "CLIENTCONSUMER", []byte("esd"))))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	got := entries[0]
	if got.Name != "win.esd" || got.LanguageCode != "en-us" || got.Architecture != "arm64" || got.URL != "http://example.test/win.esd" {
		t.Fatalf("entry = %#v", got)
	}
}

func TestSelectPrefersConsumerARM64(t *testing.T) {
	tests := []struct {
		name    string
		entries []Entry
		want    string
		wantErr bool
	}{
		{
			name: "consumer before business",
			entries: []Entry{
				testEntry("business.esd", "CLIENTBUSINESS", "ARM64", "en-us"),
				testEntry("consumer.esd", "CLIENTCONSUMER", "ARM64", "en-us"),
			},
			want: "consumer.esd",
		},
		{
			name: "ignores other architectures",
			entries: []Entry{
				testEntry("x64.esd", "CLIENTCONSUMER", "x64", "en-us"),
				testEntry("arm.esd", "CLIENTCONSUMER", "ARM64", "en-us"),
			},
			want: "arm.esd",
		},
		{
			name: "missing",
			entries: []Entry{
				testEntry("x64.esd", "CLIENTCONSUMER", "x64", "en-us"),
			},
			wantErr: true,
		},
		{
			name: "deduplicates repeated catalog entries",
			entries: []Entry{
				testEntry("consumer.esd", "CLIENTCONSUMER", "ARM64", "en-us"),
				testEntry("consumer.esd", "CLIENTCONSUMER", "ARM64", "en-us"),
			},
			want: "consumer.esd",
		},
		{
			name: "ignores incomplete entries",
			entries: []Entry{
				{Name: "missing-url.esd", LanguageCode: "en-us", Architecture: "ARM64", Size: 1, SHA1: stringsRepeat("0", 40)},
				testEntry("consumer.esd", "CLIENTCONSUMER", "ARM64", "en-us"),
			},
			want: "consumer.esd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Select(tt.entries, "en-us", "arm64")
			if tt.wantErr {
				if err == nil {
					t.Fatal("Select error = nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tt.want {
				t.Fatalf("Select() = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

func TestFetchLatest(t *testing.T) {
	esdData := []byte("fake esd bytes")
	var esdRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog.cab":
			w.Write(makeCatalogArchive(t, productsXML(serverURL(r, "/win.esd"), "ARM64", "en-us", "CLIENTCONSUMER", esdData)))
		case "/win.esd":
			esdRequests++
			w.Write(esdData)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := FetchLatest(context.Background(), Options{
		CacheDir:   t.TempDir(),
		CatalogURL: server.URL + "/catalog.cab",
		Output:     io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Entry.Name != "win.esd" {
		t.Fatalf("entry name = %q, want win.esd", result.Entry.Name)
	}
	if got, err := os.ReadFile(result.Path); err != nil || string(got) != string(esdData) {
		t.Fatalf("cached esd = %q, %v", got, err)
	}

	result, err = FetchLatest(context.Background(), Options{
		CacheDir:   filepath.Dir(result.Path),
		CatalogURL: server.URL + "/catalog.cab",
		Output:     io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if esdRequests != 1 {
		t.Fatalf("esd requests = %d, want 1", esdRequests)
	}
}

func testEntry(name, edition, arch, language string) Entry {
	return Entry{
		Name:         name,
		LanguageCode: language,
		Architecture: arch,
		Edition:      edition,
		Size:         1,
		SHA1:         stringsRepeat("0", 40),
		URL:          "http://example.test/" + name,
	}
}

func productsXML(url, arch, language, edition string, esdData []byte) string {
	sum := sha1.Sum(esdData)
	return fmt.Sprintf(`<MCT>
  <Catalogs>
    <Catalog>
      <PublishedMedia>
        <Files>
          <File>
            <FileName>win.esd</FileName>
            <LanguageCode>%s</LanguageCode>
            <Language>English</Language>
            <Edition>%s</Edition>
            <Architecture>%s</Architecture>
            <Size>%d</Size>
            <Sha1>%s</Sha1>
            <FilePath>%s</FilePath>
            <IsRetailOnly>True</IsRetailOnly>
          </File>
        </Files>
      </PublishedMedia>
    </Catalog>
  </Catalogs>
</MCT>`, language, edition, arch, len(esdData), hex.EncodeToString(sum[:]), url)
}

func makeCatalogArchive(t *testing.T, products string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "products.xml", Mode: 0644, Size: int64(len(products))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(products)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func serverURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

func stringsReader(s string) io.Reader {
	return bytes.NewBufferString(s)
}

func stringsRepeat(s string, n int) string {
	var b bytes.Buffer
	for i := 0; i < n; i++ {
		b.WriteString(s)
	}
	return b.String()
}
