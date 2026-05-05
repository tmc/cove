package secrets

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type Provider interface {
	Scheme() string
	Resolve(u *url.URL) ([]byte, error)
}

type Resolver struct {
	providers map[string]Provider
}

func NewResolver(providers ...Provider) *Resolver {
	r := &Resolver{providers: make(map[string]Provider, len(providers))}
	for _, p := range providers {
		if p == nil {
			continue
		}
		r.providers[strings.ToLower(p.Scheme())] = p
	}
	return r
}

func DefaultResolver() *Resolver {
	return NewResolver(EnvProvider{}, FileProvider{})
}

func Resolve(uri string) ([]byte, error) {
	return DefaultResolver().Resolve(uri)
}

func SupportedSchemes() []string {
	return DefaultResolver().SupportedSchemes()
}

func (r *Resolver) SupportedSchemes() []string {
	schemes := make([]string, 0, len(r.providers))
	for scheme := range r.providers {
		schemes = append(schemes, scheme)
	}
	sort.Strings(schemes)
	return schemes
}

func (r *Resolver) Resolve(uri string) ([]byte, error) {
	u, err := url.Parse(uri)
	if err != nil {
		if scheme, ok := rawScheme(uri); ok {
			return nil, r.unsupportedScheme(scheme)
		}
		return nil, fmt.Errorf("secret URI %q: %w", uri, err)
	}
	if u.Scheme == "" {
		return nil, fmt.Errorf("secret URI %q: missing scheme", uri)
	}
	scheme := strings.ToLower(u.Scheme)
	p, ok := r.providers[scheme]
	if !ok {
		return nil, r.unsupportedScheme(scheme)
	}
	value, err := p.Resolve(u)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), value...), nil
}

func (r *Resolver) unsupportedScheme(scheme string) error {
	return fmt.Errorf("unsupported secret URI scheme %q (supported: %s)", strings.ToLower(scheme), strings.Join(r.SupportedSchemes(), ", "))
}

func rawScheme(uri string) (string, bool) {
	scheme, _, ok := strings.Cut(uri, ":")
	return scheme, ok && scheme != ""
}
