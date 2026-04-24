package ociimage

import (
	"bytes"
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

// UploadBlob uploads desc using the OCI monolithic blob upload flow.
func (c RegistryClient) UploadBlob(ctx context.Context, ref Reference, desc Descriptor, r io.Reader) error {
	if desc.Digest == "" {
		return fmt.Errorf("upload blob: missing digest")
	}
	if desc.Size < 0 {
		return fmt.Errorf("upload blob: negative size %d", desc.Size)
	}
	req, err := c.newRequest(ctx, http.MethodPost, ref, "blobs/uploads/")
	if err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("upload blob: start: %w", err)
	}
	location := resp.Header.Get("Location")
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("upload blob: start registry returned %s", resp.Status)
	}
	if location == "" {
		return fmt.Errorf("upload blob: missing upload location")
	}
	u, err := uploadURL(resp.Request.URL, location, desc.Digest)
	if err != nil {
		return err
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, u, r)
	if err != nil {
		return fmt.Errorf("upload blob: request: %w", err)
	}
	put.ContentLength = desc.Size
	if c.Token != "" {
		put.Header.Set("Authorization", "Bearer "+c.Token)
	}
	put.Header.Set("Content-Type", "application/octet-stream")
	resp, err = c.httpClient().Do(put)
	if err != nil {
		return fmt.Errorf("upload blob: commit: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload blob: commit registry returned %s", resp.Status)
	}
	return nil
}

// PushManifest writes manifest to ref's tag or digest.
func (c RegistryClient) PushManifest(ctx context.Context, ref Reference, manifest Manifest) (string, error) {
	target := ref.Tag
	if ref.Digest != "" {
		target = ref.Digest
	}
	if target == "" {
		return "", fmt.Errorf("push manifest: reference must include tag or digest")
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("push manifest: encode: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPut, ref, "manifests/"+target)
	if err != nil {
		return "", err
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", MediaTypeImageManifest)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("push manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("push manifest: registry returned %s", resp.Status)
	}
	return resp.Header.Get("Docker-Content-Digest"), nil
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

func uploadURL(base *url.URL, location, digest string) (string, error) {
	u, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("upload blob: parse upload location: %w", err)
	}
	if !u.IsAbs() {
		u = base.ResolveReference(u)
	}
	q := u.Query()
	q.Set("digest", digest)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c RegistryClient) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}
