package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	keychainService     = "com.tmc.cove.gateway"
	keychainAccount     = "master-token"
	keychainLabel       = "cove gateway"
	keychainDescription = "cove gateway \u2014 grants full access to all local VMs"
	fallbackWarning     = "cove serve: storing master token as file (no keychain); pass -token-file for CI-expected file storage or run as GUI session for keychain\n"
)

// LoadOrCreateMasterToken returns a 64-hex-char master token used to authenticate
// `cove serve` HTTP clients via Authorization: Bearer <token>.
//
// Resolution order:
//  1. If tokenFile != "", use file storage at that path (create with 0600 if missing,
//     warn-but-accept if existing perms are wider than 0600).
//  2. Otherwise, attempt macOS keychain via native Security.framework bindings:
//     - KeychainGetGenericPassword to read.
//     - On miss, generate and KeychainSetGenericPassword to store.
//  3. If the keychain is unavailable (no GUI session, headless container, dlopen
//     failed), fall back to ~/.vz/gateway.token (mode 0600) and emit ONE line to
//     stderr verbatim:
//       "cove serve: storing master token as file (no keychain); pass -token-file for CI-expected file storage or run as GUI session for keychain"
//
// The token format is 32 random bytes hex-encoded (64 hex chars) from crypto/rand.
func LoadOrCreateMasterToken(tokenFile string) (string, error) {
	if tokenFile != "" {
		return loadOrCreateFileToken(tokenFile)
	}
	tok, err := keychainLoadOrCreate()
	if err != nil {
		// Keychain unavailable — fall back to ~/.vz/gateway.token.
		fmt.Fprint(os.Stderr, fallbackWarning)
		fallback, ferr := gatewayFallbackPath()
		if ferr != nil {
			return "", ferr
		}
		return loadOrCreateFileToken(fallback)
	}
	return tok, nil
}

// RotateMasterToken generates a new token and replaces the stored value via the
// same resolution rules as LoadOrCreateMasterToken. Returns the new token.
func RotateMasterToken(tokenFile string) (string, error) {
	tok, err := generateControlToken()
	if err != nil {
		return "", err
	}
	if tokenFile != "" {
		if err := writeTokenFile(tokenFile, tok); err != nil {
			return "", err
		}
		return tok, nil
	}
	if err := keychainStore(tok); err != nil {
		fmt.Fprint(os.Stderr, fallbackWarning)
		fallback, ferr := gatewayFallbackPath()
		if ferr != nil {
			return "", ferr
		}
		if err := writeTokenFile(fallback, tok); err != nil {
			return "", err
		}
	}
	return tok, nil
}

func keychainLoadOrCreate() (string, error) {
	data, err := KeychainGetGenericPassword(keychainService, keychainAccount)
	if err == nil {
		return strings.TrimRight(string(data), "\n"), nil
	}
	if !errors.Is(err, errKeychainNotFound) {
		// errKeychainUnavailable or unexpected — signal caller to fall back.
		return "", err
	}
	// Item not found — generate and store.
	tok, err := generateControlToken()
	if err != nil {
		return "", err
	}
	if err := keychainStore(tok); err != nil {
		return "", err
	}
	return tok, nil
}

func keychainStore(token string) error {
	return KeychainSetGenericPassword(
		keychainService,
		keychainAccount,
		keychainLabel,
		keychainDescription,
		[]byte(token),
	)
}

func loadOrCreateFileToken(path string) (string, error) {
	info, err := os.Stat(path)
	if err == nil {
		if info.Mode().Perm()&0177 != 0 {
			fmt.Fprintf(os.Stderr, "cove serve: warning: token file %s has permissions %04o (expected 0600)\n",
				path, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read token file: %w", err)
		}
		return strings.TrimRight(string(data), "\n"), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat token file: %w", err)
	}
	tok, err := generateControlToken()
	if err != nil {
		return "", err
	}
	if err := writeTokenFile(path, tok); err != nil {
		return "", err
	}
	return tok, nil
}

func writeTokenFile(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	return nil
}

func gatewayFallbackPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, ".vz")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create ~/.vz: %w", err)
	}
	return filepath.Join(dir, "gateway.token"), nil
}
