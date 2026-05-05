package secrets

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type EnvProvider struct{}

func (EnvProvider) Scheme() string { return "env" }

func (EnvProvider) Resolve(u *url.URL) ([]byte, error) {
	name := u.Host
	if name == "" && strings.HasPrefix(u.Path, "/") {
		name = strings.TrimPrefix(u.Path, "/")
	}
	if name == "" {
		return nil, fmt.Errorf("secret env://: empty environment variable name")
	}
	if strings.Contains(name, "/") || strings.ContainsRune(name, 0) {
		return nil, fmt.Errorf("secret env://%s: invalid environment variable name", name)
	}
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil, fmt.Errorf("secret env://%s not set", name)
	}
	return []byte(value), nil
}
