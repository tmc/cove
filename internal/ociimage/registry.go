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
	"sync"
)

// RegistryClient talks to an OCI distribution registry.
type RegistryClient struct {
	Client        *http.Client
	BaseURL       string
	Token         string
	Authorization string
	TokenCache    *RegistryTokenCache
}

// RegistryTokenCache stores registry bearer tokens between requests.
type RegistryTokenCache struct {
	mu     sync.Mutex
	tokens map[string]string
}

// NewRegistryTokenCache returns an empty registry token cache.
func NewRegistryTokenCache() *RegistryTokenCache {
	return &RegistryTokenCache{tokens: map[string]string{}}
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
	resp, err := c.do(req, ref, "pull")
	if err != nil {
		return manifest, "", fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return manifest, "", fmt.Errorf("fetch manifest: registry returned %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return manifest, "", fmt.Errorf("fetch manifest: read: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, "", fmt.Errorf("fetch manifest: decode: %w", err)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		digest = digestBytes(data)
	}
	return manifest, digest, nil
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
	resp, err := c.do(req, ref, "pull")
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
	resp, err := c.do(req, ref, "push")
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
	c.setAuthorization(put, ref, "push")
	put.Header.Set("Content-Type", "application/octet-stream")
	resp, err = c.do(put, ref, "push")
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
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", MediaTypeImageManifest)
	resp, err := c.do(req, ref, "push")
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
	resp, err := c.do(req, ref, "pull")
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

// MountBlob mounts desc's digest from source into ref's repository.
func (c RegistryClient) MountBlob(ctx context.Context, ref Reference, source Reference, desc Descriptor) (bool, error) {
	if source.Registry != "" && source.Registry != ref.Registry {
		return false, nil
	}
	if source.Repository == "" {
		return false, fmt.Errorf("mount blob: empty source repository")
	}
	if desc.Digest == "" {
		return false, fmt.Errorf("mount blob: empty digest")
	}
	req, err := c.newRequest(ctx, http.MethodPost, ref, "blobs/uploads/")
	if err != nil {
		return false, err
	}
	q := req.URL.Query()
	q.Set("mount", desc.Digest)
	q.Set("from", source.Repository)
	req.URL.RawQuery = q.Encode()
	resp, err := c.do(req, ref, "push")
	if err != nil {
		return false, fmt.Errorf("mount blob: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated:
		return true, nil
	case http.StatusAccepted:
		return false, nil
	default:
		return false, fmt.Errorf("mount blob: registry returned %s", resp.Status)
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
	c.setAuthorization(req, ref, "")
	return req, nil
}

func (c RegistryClient) setAuthorization(req *http.Request, ref Reference, action string) {
	if action != "" {
		if token := c.cachedToken(registryScope(ref, action)); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			return
		}
	}
	switch {
	case c.Authorization != "":
		req.Header.Set("Authorization", c.Authorization)
	case c.Token != "":
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c RegistryClient) do(req *http.Request, ref Reference, action string) (*http.Response, error) {
	c.setAuthorization(req, ref, action)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized || c.Token != "" || strings.HasPrefix(c.Authorization, "Bearer ") {
		return resp, nil
	}
	challenge, ok := parseBearerChallenges(resp.Header.Values("WWW-Authenticate"))
	if !ok {
		return resp, nil
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
	resp.Body.Close()
	token, err := c.fetchBearerToken(req.Context(), challenge)
	if err != nil {
		return nil, err
	}
	c.cacheToken(challenge.Scope, token)
	retry, err := cloneRegistryRequest(req)
	if err != nil {
		return nil, err
	}
	retry.Header.Set("Authorization", "Bearer "+token)
	return c.httpClient().Do(retry)
}

func (c RegistryClient) fetchBearerToken(ctx context.Context, challenge bearerChallenge) (string, error) {
	if challenge.Realm == "" {
		return "", fmt.Errorf("registry auth: missing bearer realm")
	}
	u, err := url.Parse(challenge.Realm)
	if err != nil {
		return "", fmt.Errorf("registry auth: parse bearer realm: %w", err)
	}
	q := u.Query()
	if challenge.Service != "" {
		q.Set("service", challenge.Service)
	}
	if challenge.Scope != "" {
		q.Set("scope", challenge.Scope)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("registry auth: token request: %w", err)
	}
	if strings.HasPrefix(c.Authorization, "Basic ") {
		req.Header.Set("Authorization", c.Authorization)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("registry auth: fetch bearer token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("registry auth: token service returned %s", resp.Status)
	}
	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("registry auth: decode token: %w", err)
	}
	if out.Token != "" {
		return out.Token, nil
	}
	if out.AccessToken != "" {
		return out.AccessToken, nil
	}
	return "", fmt.Errorf("registry auth: token response missing token")
}

func cloneRegistryRequest(req *http.Request) (*http.Request, error) {
	retry := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		return retry, nil
	}
	if req.GetBody == nil {
		return nil, fmt.Errorf("registry auth: cannot retry request body")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("registry auth: reopen request body: %w", err)
	}
	retry.Body = body
	return retry, nil
}

func (c RegistryClient) cachedToken(scope string) string {
	if c.TokenCache == nil || scope == "" {
		return ""
	}
	c.TokenCache.mu.Lock()
	defer c.TokenCache.mu.Unlock()
	return c.TokenCache.tokens[scope]
}

func (c RegistryClient) cacheToken(scope, token string) {
	if c.TokenCache == nil || scope == "" || token == "" {
		return
	}
	c.TokenCache.mu.Lock()
	defer c.TokenCache.mu.Unlock()
	if c.TokenCache.tokens == nil {
		c.TokenCache.tokens = map[string]string{}
	}
	c.TokenCache.tokens[scope] = token
	if repo, actions, ok := strings.Cut(strings.TrimPrefix(scope, "repository:"), ":"); ok && strings.HasPrefix(scope, "repository:") {
		for _, action := range strings.Split(actions, ",") {
			action = strings.TrimSpace(action)
			if action != "" {
				c.TokenCache.tokens[registryScope(Reference{Repository: repo}, action)] = token
			}
		}
	}
}

func registryScope(ref Reference, action string) string {
	if ref.Repository == "" || action == "" {
		return ""
	}
	return "repository:" + ref.Repository + ":" + action
}

type bearerChallenge struct {
	Realm   string
	Service string
	Scope   string
}

func parseBearerChallenge(header string) (bearerChallenge, bool) {
	header = strings.TrimSpace(header)
	for header != "" {
		scheme, rest := cutChallengeScheme(header)
		if scheme == "" {
			return bearerChallenge{}, false
		}
		paramsText, tail := cutChallengeParams(rest)
		if strings.EqualFold(scheme, "bearer") {
			params := parseChallengeParams(paramsText)
			realm := params["realm"]
			if realm == "" {
				return bearerChallenge{}, false
			}
			return bearerChallenge{
				Realm:   realm,
				Service: params["service"],
				Scope:   params["scope"],
			}, true
		}
		header = strings.TrimLeft(tail, " \t,")
	}
	return bearerChallenge{}, false
}

func parseBearerChallenges(headers []string) (bearerChallenge, bool) {
	for _, header := range headers {
		if challenge, ok := parseBearerChallenge(header); ok {
			return challenge, true
		}
	}
	return bearerChallenge{}, false
}

func cutChallengeScheme(s string) (string, string) {
	s = strings.TrimLeft(s, " \t,")
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_':
			continue
		case r == ' ' || r == '\t':
			return s[:i], strings.TrimLeft(s[i:], " \t")
		default:
			return "", ""
		}
	}
	return s, ""
}

func cutChallengeParams(s string) (string, string) {
	inQuote := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = inQuote
		case '"':
			inQuote = !inQuote
		case ',':
			if inQuote || !startsNextChallenge(s[i+1:]) {
				continue
			}
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

func startsNextChallenge(s string) bool {
	s = strings.TrimLeft(s, " \t")
	i := 0
	for ; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' ||
			c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' ||
			c == '-' ||
			c == '_'
		if !ok {
			break
		}
	}
	return i > 0 && i < len(s) && (s[i] == ' ' || s[i] == '\t')
}

func parseChallengeParams(s string) map[string]string {
	params := map[string]string{}
	for {
		s = strings.TrimLeft(s, " \t,")
		if s == "" {
			return params
		}
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return params
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = strings.TrimLeft(s[eq+1:], " \t")
		var value string
		if strings.HasPrefix(s, `"`) {
			value, s = cutQuotedChallengeValue(s[1:])
		} else {
			i := strings.IndexByte(s, ',')
			if i < 0 {
				value, s = strings.TrimSpace(s), ""
			} else {
				value, s = strings.TrimSpace(s[:i]), s[i+1:]
			}
		}
		if key != "" {
			params[key] = value
		}
	}
}

func cutQuotedChallengeValue(s string) (string, string) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
		case '"':
			return b.String(), s[i+1:]
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), ""
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
