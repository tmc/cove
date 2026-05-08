// shell_secret.go - --secret-env / --env parsing for `cove shell`.
//
// Three forms are accepted on --secret-env:
//
//	NAME=value             plain value (treated as a secret: registered
//	                       with the run-log masker and not echoed back)
//	NAME=env://VAR         resolved via internal/secrets EnvProvider
//	NAME=file:///abs/path  resolved via internal/secrets FileProvider
//
// --env (non-secret) takes only NAME=value; values are NOT registered
// with the masker. Same name on both flags: --secret-env wins, with a
// stderr warning ("--secret-env GH_TOKEN overrides --env GH_TOKEN").
//
// Empty resolved values are an error (no silent skip).
package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/tmc/vz-macos/internal/metrics"
	"github.com/tmc/vz-macos/internal/secrets"
)

// secretEnvFlag accumulates repeated --secret-env values.
type secretEnvFlag []string

func (f *secretEnvFlag) String() string     { return strings.Join(*f, ",") }
func (f *secretEnvFlag) Set(v string) error { *f = append(*f, v); return nil }

// resolveShellEnv parses --env and --secret-env entries, resolves URI
// references via internal/secrets, and registers each secret value with
// the supplied masker so the run-log redactor can scrub them from any
// host-side output. Returns the merged env map sent to the guest.
func resolveShellEnv(envs, secretEnvs []string, masker *metrics.Masker, stderr io.Writer) (map[string]string, error) {
	out := make(map[string]string, len(envs)+len(secretEnvs))

	for _, raw := range envs {
		name, value, err := parseEnvAssign(raw, false)
		if err != nil {
			return nil, fmt.Errorf("--env %s: %w", raw, err)
		}
		out[name] = value
	}

	for _, raw := range secretEnvs {
		name, value, err := parseEnvAssign(raw, true)
		if err != nil {
			return nil, fmt.Errorf("--secret-env %s: %w", raw, err)
		}
		if value == "" {
			return nil, fmt.Errorf("--secret-env %s: resolved to empty value", name)
		}
		if _, dup := out[name]; dup && stderr != nil {
			fmt.Fprintf(stderr, "cove shell: --secret-env %s overrides --env %s\n", name, name)
		}
		out[name] = value
		if masker != nil {
			masker.AddString(value)
		}
	}
	return out, nil
}

// parseEnvAssign splits a NAME=spec entry. When allowURI is true, the
// value may be a URI like env://VAR or file:///abs/path; otherwise the
// value is taken verbatim.
func parseEnvAssign(raw string, allowURI bool) (string, string, error) {
	name, spec, ok := strings.Cut(raw, "=")
	if !ok {
		return "", "", fmt.Errorf("missing '=' in %q (want NAME=value)", raw)
	}
	if name == "" {
		return "", "", fmt.Errorf("empty NAME in %q", raw)
	}
	if !allowURI {
		return name, spec, nil
	}
	if isSecretURI(spec) {
		b, err := secrets.Resolve(spec)
		if err != nil {
			return "", "", err
		}
		return name, string(b), nil
	}
	return name, spec, nil
}

// isSecretURI reports whether spec uses one of the known secret URI
// schemes. We avoid net/url here because plain values like "abc=def" can
// parse as URIs but are not what the user means.
func isSecretURI(spec string) bool {
	for _, scheme := range []string{"env://", "file://"} {
		if strings.HasPrefix(spec, scheme) {
			return true
		}
	}
	return false
}
