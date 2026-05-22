package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tmc/cove/internal/ociimage"
)

type dockerConfig struct {
	Auths       map[string]dockerAuth `json:"auths"`
	CredsStore  string                `json:"credsStore"`
	CredHelpers map[string]string     `json:"credHelpers"`
}

type dockerAuth struct {
	Auth          string `json:"auth"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	IdentityToken string `json:"identitytoken"`
}

type dockerCredential struct {
	Username string `json:"Username"`
	Secret   string `json:"Secret"`
}

func registryAuthorization(ref ociimage.Reference, explicitToken string) string {
	if token := strings.TrimSpace(explicitToken); token != "" {
		return bearerAuthorization(token)
	}
	if auth := dockerRegistryAuthorization(ref.Registry); auth != "" {
		return auth
	}
	if token := strings.TrimSpace(os.Getenv("COVE_REGISTRY_TOKEN")); token != "" {
		return bearerAuthorization(token)
	}
	if ref.Registry == "ghcr.io" {
		if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
			return bearerAuthorization(token)
		}
	}
	return ""
}

func dockerRegistryAuthorization(registry string) string {
	config, ok := readDockerConfig()
	if !ok {
		return ""
	}
	if helper := dockerCredentialHelper(config, registry); helper != "" {
		if auth := dockerCredentialHelperAuthorization(helper, registry); auth != "" {
			return auth
		}
	}
	for _, key := range dockerRegistryKeys(registry) {
		if auth, ok := config.Auths[key]; ok {
			if header := dockerAuthAuthorization(auth); header != "" {
				return header
			}
		}
	}
	return ""
}

func readDockerConfig() (dockerConfig, bool) {
	var config dockerConfig
	path := dockerConfigPath()
	if path == "" {
		return config, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return config, false
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, false
	}
	return config, true
}

func dockerConfigPath() string {
	if dir := strings.TrimSpace(os.Getenv("DOCKER_CONFIG")); dir != "" {
		return filepath.Join(dir, "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".docker", "config.json")
}

func dockerCredentialHelper(config dockerConfig, registry string) string {
	if helper := config.CredHelpers[registry]; helper != "" {
		return helper
	}
	for _, key := range dockerRegistryKeys(registry) {
		if helper := config.CredHelpers[key]; helper != "" {
			return helper
		}
	}
	return config.CredsStore
}

func dockerCredentialHelperAuthorization(helper, registry string) string {
	helper = strings.TrimSpace(helper)
	if helper == "" {
		return ""
	}
	name := helper
	if !strings.HasPrefix(name, "docker-credential-") {
		name = "docker-credential-" + name
	}
	for _, key := range dockerRegistryKeys(registry) {
		header := runDockerCredentialHelper(name, key)
		if header != "" {
			return header
		}
	}
	return ""
}

func runDockerCredentialHelper(name, server string) string {
	cmd := exec.Command(name, "get")
	cmd.Stdin = strings.NewReader(server + "\n")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	var cred dockerCredential
	if err := json.Unmarshal(stdout.Bytes(), &cred); err != nil {
		return ""
	}
	if cred.Username == "" && cred.Secret == "" {
		return ""
	}
	return basicAuthorization(cred.Username, cred.Secret)
}

func dockerAuthAuthorization(auth dockerAuth) string {
	switch {
	case strings.TrimSpace(auth.IdentityToken) != "":
		return bearerAuthorization(auth.IdentityToken)
	case strings.TrimSpace(auth.Auth) != "":
		return "Basic " + strings.TrimSpace(auth.Auth)
	case auth.Username != "" || auth.Password != "":
		return basicAuthorization(auth.Username, auth.Password)
	default:
		return ""
	}
}

func dockerRegistryKeys(registry string) []string {
	registry = strings.TrimRight(strings.TrimSpace(registry), "/")
	if registry == "" {
		return nil
	}
	return []string{
		registry,
		"https://" + registry,
		"http://" + registry,
	}
}

func bearerAuthorization(token string) string {
	return "Bearer " + strings.TrimSpace(token)
}

func basicAuthorization(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}
