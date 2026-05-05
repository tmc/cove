package secrets

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

type FileProvider struct{}

func (FileProvider) Scheme() string { return "file" }

func (FileProvider) Resolve(u *url.URL) ([]byte, error) {
	if u.Host != "" || u.Path == "" || !filepath.IsAbs(u.Path) {
		return nil, fmt.Errorf("secret %s: file URI must use an absolute path", u.String())
	}
	info, err := os.Stat(u.Path)
	if err != nil {
		return nil, fmt.Errorf("secret %s: stat %s: %w", u.String(), u.Path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("secret %s: is a directory", u.String())
	}
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		return nil, fmt.Errorf("secret %s: permissions %04o too open; require 0600 or stricter", u.String(), perm)
	}
	value, err := os.ReadFile(u.Path)
	if err != nil {
		return nil, fmt.Errorf("secret %s: read %s: %w", u.String(), u.Path, err)
	}
	return value, nil
}
