package ociimage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// RegistryClient talks to an OCI distribution registry.
type RegistryClient struct {
	Client  *http.Client
	BaseURL string
	Token   string
}

// FetchManifest fetches and decodes ref's OCI image manifest.
func (c RegistryClient) FetchManifest(ctx context.Context, ref Reference) (Manifest, string, error) {
	var manifest Manifest
	target := ref.Tag
	if ref.Digest != "" {
		target = ref.Digest
	}
	if target == "" {
		return manifest, "", fmt.Errorf("fetch manifest: reference must include tag or digest")
	}
	req, err := c.newRequest(ctx, http.MethodGet, ref, "manifests/"+target)
	if err != nil {
		return manifest, "", err
	}
	req.Header.Set("Accept", MediaTypeImageManifest)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return manifest, "", fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return manifest, "", fmt.Errorf("fetch manifest: registry returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return manifest, "", fmt.Errorf("fetch manifest: decode: %w", err)
	}
	return manifest, resp.Header.Get("Docker-Content-Digest"), nil
}

// FetchBlob opens ref's blob digest for streaming.
func (c RegistryClient) FetchBlob(ctx context.Context, ref Reference, digest string) (io.ReadCloser, error) {
	if digest == "" {
		return nil, fmt.Errorf("fetch blob: empty digest")
	}
	req, err := c.newRequest(ctx, http.MethodGet, ref, "blobs/"+digest)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch blob: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch blob: registry returned %s", resp.Status)
	}
	return resp.Body, nil
}

// BlobExists reports whether ref's registry already has digest.
func (c RegistryClient) BlobExists(ctx context.Context, ref Reference, digest string) (bool, error) {
	if digest == "" {
		return false, fmt.Errorf("blob exists: empty digest")
	}
	req, err := c.newRequest(ctx, http.MethodHead, ref, "blobs/"+digest)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return false, fmt.Errorf("blob exists: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("blob exists: registry returned %s", resp.Status)
	}
}

func (c RegistryClient) newRequest(ctx context.Context, method string, ref Reference, suffix string) (*http.Request, error) {
	u, err := c.registryURL(ref, suffix)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, fmt.Errorf("registry request: %w", err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

func (c RegistryClient) registryURL(ref Reference, suffix string) (string, error) {
	if ref.Registry == "" || ref.Repository == "" {
		return "", fmt.Errorf("registry request: incomplete reference")
	}
	base := c.BaseURL
	if base == "" {
		base = "https://" + ref.Registry
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("registry request: parse base URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("registry request: invalid base URL %q", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v2/" + ref.Repository + "/" + strings.TrimLeft(suffix, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func (c RegistryClient) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}
