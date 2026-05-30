// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// ErrUnauthenticated is returned by an Authenticator when no valid credential is
// present on the request.
var ErrUnauthenticated = errors.New("unauthenticated")

// Authenticator maps an HTTP request to an authenticated Subject. It is the SSO
// seam: BearerAuthenticator handles service-account API keys, OIDCAuthenticator
// validates signed token claims, and a real SAML/OIDC IdP integration would plug
// in here behind the same interface. It returns ErrUnauthenticated (wrapped) on
// a missing or invalid credential.
type Authenticator interface {
	Authenticate(r *http.Request) (Subject, error)
}

// BearerAuthenticator maps static service-account bearer tokens to subjects. It
// is the simplest credential path: an API key issued out of band identifies a
// service account whose grants live in the RBACStore. The zero value rejects
// everything; build one with NewBearerAuthenticator.
type BearerAuthenticator struct {
	store  *RBACStore
	tokens map[string]string // token -> subject id
}

// NewBearerAuthenticator builds an authenticator from a token->subject-id map.
// Grants for each subject are resolved from store at authenticate time so a
// regrant takes effect without reissuing tokens. A nil store leaves subjects
// without grants (deny-by-default once authorization runs).
func NewBearerAuthenticator(store *RBACStore, tokens map[string]string) *BearerAuthenticator {
	tm := make(map[string]string, len(tokens))
	for tok, id := range tokens {
		if tok != "" && id != "" {
			tm[tok] = id
		}
	}
	return &BearerAuthenticator{store: store, tokens: tm}
}

// Authenticate resolves the request's bearer token to its service-account
// subject, attaching grants from the store. A present-but-unknown token is
// compared in constant time so a wrong token does not leak via early return.
func (a *BearerAuthenticator) Authenticate(r *http.Request) (Subject, error) {
	tok := fleetproto.BearerToken(r)
	if tok == "" {
		return Subject{}, fmt.Errorf("bearer: %w", ErrUnauthenticated)
	}
	matched := ""
	for cand, id := range a.tokens {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(cand)) == 1 {
			matched = id
		}
	}
	if matched == "" {
		return Subject{}, fmt.Errorf("bearer: %w", ErrUnauthenticated)
	}
	if a.store != nil {
		if subj, ok := a.store.Subject(matched); ok {
			return subj, nil
		}
	}
	return Subject{ID: matched}, nil
}

// FirstAuthenticator returns an Authenticator that tries each delegate in order
// and returns the first successful Subject. It lets a deployment accept both
// machine service-account bearer keys and human SSO tokens on one surface. With
// no delegates it rejects every request. The combined error wraps
// ErrUnauthenticated.
func FirstAuthenticator(auths ...Authenticator) Authenticator {
	return chainAuthenticator(auths)
}

// chainAuthenticator is the slice-backed implementation of FirstAuthenticator.
type chainAuthenticator []Authenticator

// Authenticate returns the first delegate's successful Subject, or
// ErrUnauthenticated when none accept the request.
func (c chainAuthenticator) Authenticate(r *http.Request) (Subject, error) {
	for _, a := range c {
		if a == nil {
			continue
		}
		subj, err := a.Authenticate(r)
		if err == nil {
			return subj, nil
		}
	}
	return Subject{}, fmt.Errorf("no authenticator accepted request: %w", ErrUnauthenticated)
}

// ssoClaims is the minimal JWT-shaped claim set OIDCAuthenticator understands:
// the subject, expiry, and per-namespace role grants needed to build a Subject.
// The standard registered claims (iss, aud, etc.) are intentionally not modeled.
// The signature verification in OIDCAuthenticator is real (it fails closed on
// alg:none, alg mismatch, a bad signature, expiry, or an empty subject); only
// the claim set here is minimal. Grants map namespace -> role string.
type ssoClaims struct {
	Subject   string          `json:"sub"`
	ExpiresAt int64           `json:"exp,omitempty"`
	Grants    map[string]Role `json:"grants,omitempty"`
}

// ssoHeader is the JWT-shaped header. alg selects the verification primitive:
// "HS256" (HMAC-SHA256) or "EdDSA" (ed25519). "none" is always rejected.
type ssoHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ,omitempty"`
}

// OIDCVerifier validates a token's signature and decodes its claims. It is the
// pluggable trust root: HMACVerifier shares a symmetric secret, Ed25519Verifier
// holds an issuer public key, and a production deployment would replace either
// with a JWKS-backed verifier fetched from the IdP's discovery document. The
// header.payload.sig token shape mirrors a JWT so the wire format is unchanged
// when a real verifier is swapped in.
type OIDCVerifier interface {
	// Alg returns the JOSE algorithm name this verifier expects in the token
	// header ("HS256" or "EdDSA").
	Alg() string
	// Verify checks that sig authenticates signingInput (the raw
	// "header.payload" bytes). It returns nil on a valid signature.
	Verify(signingInput, sig []byte) error
}

// HMACVerifier verifies HS256 tokens with a shared secret. Suitable for
// service-to-service trust where a symmetric key can be provisioned to both the
// IdP shim and the controller.
type HMACVerifier struct {
	Secret []byte
}

// Alg reports the HS256 algorithm name.
func (HMACVerifier) Alg() string { return "HS256" }

// Verify checks the HMAC-SHA256 tag over signingInput in constant time.
func (v HMACVerifier) Verify(signingInput, sig []byte) error {
	mac := hmac.New(sha256.New, v.Secret)
	mac.Write(signingInput)
	want := mac.Sum(nil)
	if subtle.ConstantTimeCompare(want, sig) != 1 {
		return fmt.Errorf("hmac verify: %w", ErrUnauthenticated)
	}
	return nil
}

// Ed25519Verifier verifies EdDSA tokens against an issuer public key. This is
// the shape a JWKS-published asymmetric key would take.
type Ed25519Verifier struct {
	PublicKey ed25519.PublicKey
}

// Alg reports the EdDSA algorithm name.
func (Ed25519Verifier) Alg() string { return "EdDSA" }

// Verify checks the ed25519 signature over signingInput.
func (v Ed25519Verifier) Verify(signingInput, sig []byte) error {
	if !ed25519.Verify(v.PublicKey, signingInput, sig) {
		return fmt.Errorf("ed25519 verify: %w", ErrUnauthenticated)
	}
	return nil
}

// OIDCAuthenticator validates a signed JWT-shaped token into a Subject. It is a
// seam, not a full IdP integration: it verifies a self-contained token's
// signature and claims rather than performing an OIDC discovery/JWKS exchange.
// To wire a real IdP, supply a JWKS-backed OIDCVerifier and (optionally) check
// issuer/audience in a wrapping verifier. The zero value is not usable; build
// one with NewOIDCAuthenticator.
type OIDCAuthenticator struct {
	verifier OIDCVerifier
	// Now is injected for testability; nil falls back to time.Now.
	Now func() time.Time
}

// NewOIDCAuthenticator builds the authenticator over a verifier (the trust
// root). The verifier's Alg must match the token header's alg or verification
// fails closed.
func NewOIDCAuthenticator(verifier OIDCVerifier) *OIDCAuthenticator {
	return &OIDCAuthenticator{verifier: verifier}
}

func (a *OIDCAuthenticator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Authenticate parses, verifies, and maps the bearer token's claims into a
// Subject. The token is a JWT-shaped base64url(header).base64url(payload).sig.
// The "none" algorithm and an algorithm mismatch are rejected, an expired token
// is rejected, and a missing subject is rejected.
func (a *OIDCAuthenticator) Authenticate(r *http.Request) (Subject, error) {
	tok := fleetproto.BearerToken(r)
	if tok == "" {
		return Subject{}, fmt.Errorf("oidc: %w", ErrUnauthenticated)
	}
	return a.parse(tok)
}

// parse is the token-verification core, separated from the request for testing.
func (a *OIDCAuthenticator) parse(tok string) (Subject, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return Subject{}, fmt.Errorf("oidc: malformed token: %w", ErrUnauthenticated)
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Subject{}, fmt.Errorf("oidc: decode header: %w", ErrUnauthenticated)
	}
	var hdr ssoHeader
	if err := json.Unmarshal(headerJSON, &hdr); err != nil {
		return Subject{}, fmt.Errorf("oidc: parse header: %w", ErrUnauthenticated)
	}
	if strings.EqualFold(hdr.Alg, "none") {
		return Subject{}, fmt.Errorf("oidc: alg none rejected: %w", ErrUnauthenticated)
	}
	if a.verifier == nil || !strings.EqualFold(hdr.Alg, a.verifier.Alg()) {
		return Subject{}, fmt.Errorf("oidc: alg mismatch: %w", ErrUnauthenticated)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Subject{}, fmt.Errorf("oidc: decode signature: %w", ErrUnauthenticated)
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	if err := a.verifier.Verify(signingInput, sig); err != nil {
		return Subject{}, err
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Subject{}, fmt.Errorf("oidc: decode payload: %w", ErrUnauthenticated)
	}
	var claims ssoClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return Subject{}, fmt.Errorf("oidc: parse claims: %w", ErrUnauthenticated)
	}
	if claims.Subject == "" {
		return Subject{}, fmt.Errorf("oidc: missing subject: %w", ErrUnauthenticated)
	}
	if claims.ExpiresAt != 0 && a.now().After(time.Unix(claims.ExpiresAt, 0)) {
		return Subject{}, fmt.Errorf("oidc: token expired: %w", ErrUnauthenticated)
	}
	return subjectFromClaims(claims), nil
}

// subjectFromClaims builds a Subject from verified claims, translating the
// namespace->role claim map into Grants.
func subjectFromClaims(c ssoClaims) Subject {
	subj := Subject{ID: c.Subject}
	for ns, role := range c.Grants {
		if ns == "" {
			ns = NamespaceWildcard
		}
		subj.Grants = append(subj.Grants, Grant{Namespace: ns, Role: role})
	}
	return subj
}

// SignSSOToken mints a JWT-shaped token for the given claims, signing the
// header.payload with sign. It is a test/dev helper that mirrors what a real IdP
// would emit; production tokens come from the IdP, not from this function.
func SignSSOToken(alg string, claims ssoClaimsInput, sign func(signingInput []byte) []byte) (string, error) {
	hdr := ssoHeader{Alg: alg, Typ: "JWT"}
	hdrJSON, err := json.Marshal(hdr)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}
	c := ssoClaims{Subject: claims.Subject, ExpiresAt: claims.ExpiresAt, Grants: claims.Grants}
	claimsJSON, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}
	h := base64.RawURLEncoding.EncodeToString(hdrJSON)
	p := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := h + "." + p
	sig := sign([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// ssoClaimsInput is the public shape callers pass to SignSSOToken.
type ssoClaimsInput struct {
	Subject   string
	ExpiresAt int64
	Grants    map[string]Role
}

// HMACSigner returns a sign function for SignSSOToken matching HMACVerifier.
func HMACSigner(secret []byte) func(signingInput []byte) []byte {
	return func(signingInput []byte) []byte {
		mac := hmac.New(sha256.New, secret)
		mac.Write(signingInput)
		return mac.Sum(nil)
	}
}

// Ed25519Signer returns a sign function for SignSSOToken matching
// Ed25519Verifier.
func Ed25519Signer(priv ed25519.PrivateKey) func(signingInput []byte) []byte {
	return func(signingInput []byte) []byte {
		return ed25519.Sign(priv, signingInput)
	}
}
