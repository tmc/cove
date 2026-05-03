package esd

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const Windows11CatalogURL = "https://go.microsoft.com/fwlink?linkid=2156292"

type Entry struct {
	Name         string
	LanguageCode string
	Architecture string
	Edition      string
	Size         int64
	SHA1         string
	URL          string
	IsRetailOnly bool
}

type Options struct {
	CacheDir     string
	CatalogURL   string
	LanguageCode string
	Architecture string
	TarPath      string
	Client       *http.Client
	Output       io.Writer
}

type Result struct {
	Entry Entry
	Path  string
}

func FetchLatest(ctx context.Context, opts Options) (Result, error) {
	opts = opts.withDefaults()
	if err := os.MkdirAll(opts.CacheDir, 0755); err != nil {
		return Result{}, fmt.Errorf("create windows esd cache: %w", err)
	}

	catalogPath := filepath.Join(opts.CacheDir, "windows11-products.cab")
	if opts.Output != nil {
		fmt.Fprintf(opts.Output, "Fetching Windows ESD catalog: %s\n", opts.CatalogURL)
	}
	if err := download(ctx, opts.client(), opts.CatalogURL, catalogPath, "", 0, opts.Output); err != nil {
		return Result{}, fmt.Errorf("download windows esd catalog: %w", err)
	}

	products, err := extractProductsXML(ctx, opts.TarPath, catalogPath)
	if err != nil {
		return Result{}, fmt.Errorf("extract windows esd catalog: %w", err)
	}
	entries, err := ParseProductsXML(bytes.NewReader(products))
	if err != nil {
		return Result{}, fmt.Errorf("parse windows esd catalog: %w", err)
	}
	entry, err := Select(entries, opts.LanguageCode, opts.Architecture)
	if err != nil {
		return Result{}, err
	}

	esdPath := filepath.Join(opts.CacheDir, entry.Name)
	if complete(esdPath, entry.SHA1, entry.Size) {
		if opts.Output != nil {
			fmt.Fprintf(opts.Output, "Using cached Windows ESD: %s\n", esdPath)
		}
		return Result{Entry: entry, Path: esdPath}, nil
	}

	if opts.Output != nil {
		fmt.Fprintf(opts.Output, "Downloading Windows ESD: %s\n", entry.Name)
	}
	if err := download(ctx, opts.client(), entry.URL, esdPath, entry.SHA1, entry.Size, opts.Output); err != nil {
		return Result{}, fmt.Errorf("download windows esd: %w", err)
	}
	return Result{Entry: entry, Path: esdPath}, nil
}

func ParseProductsXML(r io.Reader) ([]Entry, error) {
	var doc struct {
		Files []struct {
			Name         string `xml:"FileName"`
			LanguageCode string `xml:"LanguageCode"`
			Architecture string `xml:"Architecture"`
			Edition      string `xml:"Edition"`
			Size         int64  `xml:"Size"`
			SHA1         string `xml:"Sha1"`
			URL          string `xml:"FilePath"`
			IsRetailOnly string `xml:"IsRetailOnly"`
		} `xml:"Catalogs>Catalog>PublishedMedia>Files>File"`
	}
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(doc.Files))
	for _, f := range doc.Files {
		entries = append(entries, Entry{
			Name:         strings.TrimSpace(f.Name),
			LanguageCode: strings.TrimSpace(f.LanguageCode),
			Architecture: strings.TrimSpace(f.Architecture),
			Edition:      strings.TrimSpace(f.Edition),
			Size:         f.Size,
			SHA1:         strings.ToLower(strings.TrimSpace(f.SHA1)),
			URL:          strings.TrimSpace(f.URL),
			IsRetailOnly: strings.EqualFold(strings.TrimSpace(f.IsRetailOnly), "true"),
		})
	}
	return entries, nil
}

func Select(entries []Entry, languageCode, architecture string) (Entry, error) {
	languageCode = strings.ToLower(strings.TrimSpace(languageCode))
	architecture = normalizeArch(architecture)
	var matches []Entry
	seen := map[string]bool{}
	for _, e := range entries {
		if strings.ToLower(e.LanguageCode) != languageCode {
			continue
		}
		if normalizeArch(e.Architecture) != architecture {
			continue
		}
		if e.Name == "" || e.URL == "" || e.Size <= 0 || e.SHA1 == "" {
			continue
		}
		key := e.SHA1 + "\x00" + e.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		matches = append(matches, e)
	}
	if len(matches) == 0 {
		return Entry{}, fmt.Errorf("no windows esd for language %q architecture %q", languageCode, architecture)
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return entryRank(matches[i]) < entryRank(matches[j])
	})
	return matches[0], nil
}

func entryRank(e Entry) int {
	name := strings.ToLower(e.Name)
	edition := strings.ToLower(e.Edition)
	switch {
	case strings.Contains(name, "clientconsumer") || strings.Contains(edition, "clientconsumer"):
		return 0
	case strings.Contains(name, "clientbusiness") || strings.Contains(edition, "clientbusiness"):
		return 1
	default:
		return 2
	}
}

func normalizeArch(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "a64", "arm64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}

func (o Options) withDefaults() Options {
	if o.CatalogURL == "" {
		o.CatalogURL = Windows11CatalogURL
	}
	if o.LanguageCode == "" {
		o.LanguageCode = "en-us"
	}
	if o.Architecture == "" {
		o.Architecture = "arm64"
	}
	if o.TarPath == "" {
		o.TarPath = "tar"
	}
	return o
}

func (o Options) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return http.DefaultClient
}

func extractProductsXML(ctx context.Context, tarPath, catalogPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, tarPath, "-xOf", catalogPath, "products.xml")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("products.xml is empty")
	}
	return out, nil
}

func complete(path, wantSHA1 string, wantSize int64) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() != wantSize {
		return false
	}
	got, err := fileSHA1(path)
	return err == nil && strings.EqualFold(got, wantSHA1)
}

func download(ctx context.Context, client *http.Client, url, dest, wantSHA1 string, wantSize int64, output io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".*.partial")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hash := sha1.New()
	var written int64
	reader := io.Reader(resp.Body)
	if output != nil && responseSize(resp, wantSize) > 100*1024*1024 {
		reader = &progressReader{
			r:      resp.Body,
			total:  responseSize(resp, wantSize),
			output: output,
			start:  time.Now(),
		}
	}
	written, err = io.Copy(tmp, io.TeeReader(reader, hash))
	if err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if wantSize > 0 && written != wantSize {
		return fmt.Errorf("downloaded size %d, want %d", written, wantSize)
	}
	gotSHA1 := hex.EncodeToString(hash.Sum(nil))
	if wantSHA1 != "" && !strings.EqualFold(gotSHA1, wantSHA1) {
		return fmt.Errorf("downloaded sha1 %s, want %s", gotSHA1, wantSHA1)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return err
	}
	if output != nil && wantSize > 0 {
		fmt.Fprintf(output, "Downloaded %.1f GB: %s\n", float64(written)/(1024*1024*1024), dest)
	}
	return nil
}

func responseSize(resp *http.Response, fallback int64) int64 {
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return fallback
}

func fileSHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progressReader struct {
	r      io.Reader
	total  int64
	done   int64
	output io.Writer
	start  time.Time
	last   time.Time
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.done += int64(n)
	now := time.Now()
	if n > 0 && (r.last.IsZero() || now.Sub(r.last) >= time.Second || err == io.EOF) {
		r.last = now
		if r.total > 0 {
			pct := 100 * float64(r.done) / float64(r.total)
			fmt.Fprintf(r.output, "  %.1f%% %.1f/%.1f GB\n", pct, float64(r.done)/(1024*1024*1024), float64(r.total)/(1024*1024*1024))
		} else {
			fmt.Fprintf(r.output, "  %.1f GB\n", float64(r.done)/(1024*1024*1024))
		}
	}
	return n, err
}
