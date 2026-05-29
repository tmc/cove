package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runInstallPreflight(env commandEnv) error {
	switch {
	case strings.TrimSpace(isoPath) != "":
		path, stop, err := installPreflightMediaAccess("iso", isoPath)
		if err != nil {
			return err
		}
		defer stop()
		if err := readInstallPreflightByte(path); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "install preflight: iso readable: %s\n", path)
		return nil
	case strings.TrimSpace(ipswPath) != "":
		path, stop, err := installPreflightMediaAccess("ipsw", ipswPath)
		if err != nil {
			return err
		}
		defer stop()
		if err := readInstallPreflightByte(path); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "install preflight: ipsw readable: %s\n", path)
		return nil
	default:
		return fmt.Errorf("install preflight requires -iso or -ipsw")
	}
}

func installPreflightMediaAccess(kind, rawPath string) (path string, stop func(), err error) {
	path, err = localInstallMediaPath(kind, rawPath)
	if err != nil {
		return "", nil, err
	}
	if !appleAppSandboxActive() {
		return path, func() {}, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve install media: %w", err)
	}
	storePath, err := defaultSecurityBookmarkStorePath()
	if err != nil {
		return "", nil, err
	}
	key := kind + ":" + abs
	access, err := resolveSecurityBookmarkAccessFromStore(storePath, key)
	if err != nil {
		return "", nil, powerboxGrantRequiredKind("read media", key, kind, storePath)
	}
	if !powerboxFileExtensionAllowed(access.Path, []string{kind}) {
		access.Stop()
		return "", nil, fmt.Errorf("bookmark %s resolved to unsupported media path: %s", key, access.Path)
	}
	return access.Path, access.Stop, nil
}

func localInstallMediaPath(kind, rawPath string) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("%s path required", kind)
	}
	switch kind {
	case "ipsw":
		src, err := parseIPSWSource(rawPath)
		if err != nil {
			return "", err
		}
		if src.IsURL {
			return "", fmt.Errorf("install preflight requires a local IPSW path")
		}
		return src.Path, nil
	case "iso":
		if isURL(rawPath) {
			return "", fmt.Errorf("install preflight requires a local ISO path")
		}
		return rawPath, nil
	default:
		return "", fmt.Errorf("unknown install media kind %q", kind)
	}
}

func readInstallPreflightByte(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open install media: %w", err)
	}
	defer f.Close()
	var buf [1]byte
	_, err = f.Read(buf[:])
	if err != nil && err != io.EOF {
		return fmt.Errorf("read install media: %w", err)
	}
	return nil
}
