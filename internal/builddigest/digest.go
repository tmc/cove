// Package builddigest contains digest helpers for cove build cache inputs.
package builddigest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

// URL returns the sha256 digest of the response body at rawurl.
func URL(ctx context.Context, client *http.Client, rawurl string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return "", fmt.Errorf("cache-url %s: %w", rawurl, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cache-url %s: %w", rawurl, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cache-url %s: returned %s", rawurl, resp.Status)
	}
	return Reader(resp.Body)
}

// File returns the sha256 digest of path.
func File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cache-file %s: %w", path, err)
	}
	defer f.Close()
	return Reader(f)
}

// Reader returns the sha256 digest of r.
func Reader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Bytes returns the sha256 digest of data.
func Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// WriteKV appends a canonical key-value line to b.
func WriteKV(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(value)
	b.WriteByte('\n')
}

// WriteMap appends canonical sorted key-value lines to b.
func WriteMap(b *strings.Builder, key string, m map[string]string) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		WriteKV(b, key+":"+k, m[k])
	}
}

// WriteList appends canonical list entries to b.
func WriteList(b *strings.Builder, key string, list []string) {
	for _, v := range list {
		WriteKV(b, key, v)
	}
}
