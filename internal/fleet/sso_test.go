// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"crypto/ed25519"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// bearerReq builds a request carrying an Authorization: Bearer token.
func bearerReq(token string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/v1/sandboxes", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestBearerAuthenticator(t *testing.T) {
	store, _ := NewRBACStore("")
	if err := store.Grant("svc-ci", RoleOperator, "team-a"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	auth := NewBearerAuthenticator(store, map[string]string{"tok-ci": "svc-ci"})

	tests := []struct {
		name    string
		token   string
		wantID  string
		wantErr bool
	}{
		{name: "valid token maps to subject with grants", token: "tok-ci", wantID: "svc-ci"},
		{name: "unknown token rejected", token: "tok-bogus", wantErr: true},
		{name: "missing token rejected", token: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subj, err := auth.Authenticate(bearerReq(tt.token))
			if tt.wantErr {
				if !errors.Is(err, ErrUnauthenticated) {
					t.Fatalf("err = %v, want ErrUnauthenticated", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Authenticate: %v", err)
			}
			if subj.ID != tt.wantID {
				t.Errorf("subject id = %q, want %q", subj.ID, tt.wantID)
			}
			var p Policy
			if !p.Can(subj, ActionStop, Resource{Namespace: "team-a"}) {
				t.Error("bearer subject should carry team-a operator grant")
			}
		})
	}
}

func TestOIDCAuthenticatorHMAC(t *testing.T) {
	secret := []byte("super-secret")
	auth := NewOIDCAuthenticator(HMACVerifier{Secret: secret})

	tok, err := SignSSOToken("HS256", ssoClaimsInput{
		Subject: "alice@example.com",
		Grants:  map[string]Role{"team-a": RoleAdmin},
	}, HMACSigner(secret))
	if err != nil {
		t.Fatalf("SignSSOToken: %v", err)
	}

	subj, err := auth.Authenticate(bearerReq(tok))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if subj.ID != "alice@example.com" {
		t.Errorf("subject id = %q", subj.ID)
	}
	var p Policy
	// alice is admin only in team-a, not wildcard, so push-policy must deny.
	if p.Can(subj, ActionPushPolicy, Resource{Namespace: NamespaceWildcard}) {
		t.Error("team-a admin must not push fleet policy")
	}
	if p.Can(subj, ActionStop, Resource{Namespace: "team-b"}) {
		t.Error("alice (team-a admin) must not act on team-b")
	}
	if !p.Can(subj, ActionStop, Resource{Namespace: "team-a"}) {
		t.Error("alice (team-a admin) should act on team-a")
	}
}

func TestOIDCAuthenticatorEd25519(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	auth := NewOIDCAuthenticator(Ed25519Verifier{PublicKey: pub})
	tok, err := SignSSOToken("EdDSA", ssoClaimsInput{Subject: "bob"}, Ed25519Signer(priv))
	if err != nil {
		t.Fatalf("SignSSOToken: %v", err)
	}
	subj, err := auth.Authenticate(bearerReq(tok))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if subj.ID != "bob" {
		t.Errorf("subject id = %q, want bob", subj.ID)
	}
}

func TestOIDCAuthenticatorRejections(t *testing.T) {
	secret := []byte("k")
	auth := NewOIDCAuthenticator(HMACVerifier{Secret: secret})
	auth.Now = func() time.Time { return time.Unix(1_000_000, 0) }

	// Tampered signature: sign with the right secret then mangle the payload.
	good, _ := SignSSOToken("HS256", ssoClaimsInput{Subject: "alice"}, HMACSigner(secret))
	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + parts[1] + "x." + parts[2]

	wrongSecret, _ := SignSSOToken("HS256", ssoClaimsInput{Subject: "alice"}, HMACSigner([]byte("other")))
	expired, _ := SignSSOToken("HS256", ssoClaimsInput{Subject: "alice", ExpiresAt: 1}, HMACSigner(secret))
	noneAlg, _ := SignSSOToken("none", ssoClaimsInput{Subject: "alice"}, func([]byte) []byte { return nil })
	noSub, _ := SignSSOToken("HS256", ssoClaimsInput{Subject: ""}, HMACSigner(secret))

	tests := []struct {
		name string
		tok  string
	}{
		{name: "empty token", tok: ""},
		{name: "malformed (two parts)", tok: "a.b"},
		{name: "tampered payload", tok: tampered},
		{name: "wrong secret", tok: wrongSecret},
		{name: "expired", tok: expired},
		{name: "alg none", tok: noneAlg},
		{name: "missing subject", tok: noSub},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := auth.Authenticate(bearerReq(tt.tok))
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("err = %v, want ErrUnauthenticated", err)
			}
		})
	}
}

func TestOIDCAuthenticatorAlgMismatch(t *testing.T) {
	// An EdDSA-signed token presented to an HMAC verifier must fail closed on the
	// algorithm check rather than attempting an HMAC compare.
	_, priv, _ := ed25519.GenerateKey(nil)
	tok, _ := SignSSOToken("EdDSA", ssoClaimsInput{Subject: "bob"}, Ed25519Signer(priv))
	auth := NewOIDCAuthenticator(HMACVerifier{Secret: []byte("k")})
	if _, err := auth.Authenticate(bearerReq(tok)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("alg mismatch should reject: %v", err)
	}
}

func TestFirstAuthenticator(t *testing.T) {
	store, _ := NewRBACStore("")
	_ = store.Grant("svc", RoleViewer, "team-a")
	bearer := NewBearerAuthenticator(store, map[string]string{"machine-key": "svc"})
	secret := []byte("hmac")
	oidc := NewOIDCAuthenticator(HMACVerifier{Secret: secret})
	chain := FirstAuthenticator(bearer, oidc)

	// Bearer path.
	if subj, err := chain.Authenticate(bearerReq("machine-key")); err != nil || subj.ID != "svc" {
		t.Fatalf("bearer path: subj=%+v err=%v", subj, err)
	}
	// SSO path (bearer rejects, oidc accepts).
	tok, _ := SignSSOToken("HS256", ssoClaimsInput{Subject: "human"}, HMACSigner(secret))
	if subj, err := chain.Authenticate(bearerReq(tok)); err != nil || subj.ID != "human" {
		t.Fatalf("sso path: subj=%+v err=%v", subj, err)
	}
	// Neither accepts.
	if _, err := chain.Authenticate(bearerReq("garbage")); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("chain should reject unknown credential: %v", err)
	}
	// Empty chain rejects.
	if _, err := FirstAuthenticator().Authenticate(bearerReq("x")); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("empty chain should reject: %v", err)
	}
}
