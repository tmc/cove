// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
//
// cove-fleetd is the stateful fleet controller. It accepts worker dial-ins over
// the four-verb HTTP protocol (register, heartbeat, await-assignment,
// report-status), persists host inventory to a single embedded JSON store
// guarded by a mutex, and serves an in-memory assignment queue per host.
//
// The controller is a deliberate single point of failure: back up the -state
// file. Bearer-token auth (register token, then per-host lease) and SSO JWTs
// cross the wire, so serve over TLS in production: set -tls-cert and -tls-key
// (PEM paths) to enable TLS, and -tls-client-ca to additionally require and
// verify worker client certificates (mTLS). Plaintext HTTP is the dev-only
// default when neither -tls-cert nor -tls-key is set; setting exactly one of
// them is rejected rather than silently falling back to plaintext.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/cove/internal/fleet"
)

func main() {
	// Subcommands: with no subcommand (or "serve") cove-fleetd runs the
	// controller. "push" is an operator client that triggers a fleet-wide policy
	// or image-gc push against a running controller and prints the aggregated
	// per-host outcome.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "push":
			if err := runPush(os.Args[2:]); err != nil {
				slog.Error("push", slog.Any("err", err))
				os.Exit(1)
			}
			return
		case "serve":
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
	}

	listen := flag.String("listen", "127.0.0.1:9878", "controller listen address")
	state := flag.String("state", defaultStatePath(), "fleet state file path")
	token := flag.String("token", os.Getenv("COVE_FLEET_TOKEN"), "one-time worker register token (empty disables register auth)")
	apiKeys := flag.String("api-keys", os.Getenv("COVE_FLEET_API_KEYS"), "comma-separated bearer API keys for the hosted /v1/sandboxes API (empty leaves it open)")
	rbacState := flag.String("rbac-state", defaultRBACPath(), "RBAC grant store file path")
	auditPath := flag.String("audit-log", defaultAuditPath(), "tamper-evident fleet audit log path (empty disables persistence)")
	saAccounts := flag.String("service-accounts", os.Getenv("COVE_FLEET_SERVICE_ACCOUNTS"), "comma-separated token=subject service-account pairs (empty leaves RBAC middleware off)")
	oidcSecret := flag.String("oidc-hmac-secret", os.Getenv("COVE_FLEET_OIDC_HMAC_SECRET"), "shared HS256 secret for SSO token verification (empty uses service accounts only)")
	tlsCert := flag.String("tls-cert", os.Getenv("COVE_FLEET_TLS_CERT"), "server certificate PEM path (with -tls-key enables TLS; empty serves plaintext)")
	tlsKey := flag.String("tls-key", os.Getenv("COVE_FLEET_TLS_KEY"), "server key PEM path (with -tls-cert enables TLS; empty serves plaintext)")
	tlsClientCA := flag.String("tls-client-ca", os.Getenv("COVE_FLEET_TLS_CLIENT_CA"), "PEM CA bundle to require and verify worker client certs (mTLS); empty disables mTLS")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil)).With(slog.String("component", "cove-fleetd"))
	slog.SetDefault(logger)

	reg, err := fleet.NewHostRegistry(*state, *token)
	if err != nil {
		slog.Error("open registry", slog.Any("err", err))
		os.Exit(1)
	}
	controller := fleet.NewController(reg)

	// Hosted REST API (paid): /v1/sandboxes, sharing the controller registry as
	// the scheduler and cordoner. Mounted on the same mux as the worker verbs.
	store := fleet.NewSandboxStore()
	ledger := fleet.NewUsageLedger()
	hosted := fleet.NewHostedAPI(reg, store, ledger, reg, splitKeys(*apiKeys))

	// RBAC / SSO / audit (paid). When service accounts or an OIDC secret are
	// configured, every request to the hosted /v1 surface and the operator
	// endpoints passes through authenticate -> authorize -> audit before the
	// handler. With neither configured the middleware is left off and the
	// underlying API-key/operator-token checks apply as before.
	ac, err := buildAccessControl(*rbacState, *auditPath, *saAccounts, *oidcSecret)
	if err != nil {
		slog.Error("build access control", slog.Any("err", err))
		os.Exit(1)
	}

	mux := http.NewServeMux()
	if ac != nil {
		// Worker dial-ins stay unwrapped (per-host lease auth); the hosted /v1
		// surface and the operator endpoints go behind the RBAC/audit middleware.
		controller.RegisterWorkerHandlers(mux)
		mountProtected(mux, ac, controller, hosted)
		slog.Info("fleet RBAC/SSO/audit middleware enabled", slog.String("audit", *auditPath), slog.String("rbac", *rbacState))
	} else {
		controller.RegisterHandlers(mux)
		hosted.RegisterHandlers(mux)
	}

	tlsConfig, err := buildServerTLSConfig(*tlsCert, *tlsKey, *tlsClientCA)
	if err != nil {
		slog.Error("tls config", slog.Any("err", err))
		os.Exit(1)
	}

	server := &http.Server{Addr: *listen, Handler: mux, TLSConfig: tlsConfig}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if tlsConfig != nil {
		mtls := tlsConfig.ClientAuth == tls.RequireAndVerifyClientCert
		slog.Info("fleet controller listening", slog.String("listen", *listen), slog.String("state", *state), slog.String("mode", "tls"), slog.Bool("mtls", mtls))
		// Certificates are already loaded into TLSConfig, so the file args are empty.
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			slog.Error("serve", slog.Any("err", err))
			os.Exit(1)
		}
		return
	}

	slog.Info("fleet controller listening", slog.String("listen", *listen), slog.String("state", *state), slog.String("mode", "plaintext"))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", slog.Any("err", err))
		os.Exit(1)
	}
}

// buildServerTLSConfig assembles the controller's server TLS configuration. It
// returns (nil, nil) when neither certFile nor keyFile is set, signalling the
// dev-only plaintext path. It returns an error when exactly one of certFile and
// keyFile is set, refusing to silently fall back to plaintext after a partial
// TLS configuration. When both are set it loads the key pair at MinVersion
// TLS 1.2; a non-empty clientCAFile additionally requires and verifies worker
// client certificates against that CA bundle (mTLS).
func buildServerTLSConfig(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("tls: -tls-cert and -tls-key must be set together")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("tls: load server cert: %w", err)
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if clientCAFile != "" {
		pem, err := os.ReadFile(clientCAFile)
		if err != nil {
			return nil, fmt.Errorf("tls: read client ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls: parse client ca %q: no certificates found", clientCAFile)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

func defaultStatePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "fleetd", "state.json")
}

func defaultRBACPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "fleetd", "rbac.json")
}

func defaultAuditPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "fleetd", "audit.jsonl")
}

// buildAccessControl assembles the RBAC/SSO/audit middleware. It returns nil
// (middleware disabled) when neither service accounts nor an OIDC secret are
// configured. Bearer service accounts and SSO tokens are tried in order, so a
// deployment may mix human SSO and machine service-account credentials.
func buildAccessControl(rbacState, auditPath, serviceAccounts, oidcSecret string) (*fleet.AccessControl, error) {
	accounts := parsePairs(serviceAccounts)
	if len(accounts) == 0 && strings.TrimSpace(oidcSecret) == "" {
		return nil, nil
	}
	store, err := fleet.NewRBACStore(rbacState)
	if err != nil {
		return nil, err
	}
	audit, err := fleet.NewAuditLog(auditPath)
	if err != nil {
		return nil, err
	}
	var auths []fleet.Authenticator
	if len(accounts) > 0 {
		auths = append(auths, fleet.NewBearerAuthenticator(store, accounts))
	}
	if s := strings.TrimSpace(oidcSecret); s != "" {
		auths = append(auths, fleet.NewOIDCAuthenticator(fleet.HMACVerifier{Secret: []byte(s)}))
	}
	return fleet.NewAccessControl(fleet.FirstAuthenticator(auths...), store, audit), nil
}

// mountProtected wraps the hosted /v1 surface and the controller's operator
// endpoints in the access-control middleware before mounting them on mux.
func mountProtected(mux *http.ServeMux, ac *fleet.AccessControl, controller *fleet.Controller, hosted *fleet.HostedAPI) {
	hostedMux := http.NewServeMux()
	hosted.RegisterHandlers(hostedMux)
	mux.Handle(fleet.PathSandboxes, ac.Protect(ac.SandboxResolver, hostedMux))
	mux.Handle(fleet.PathSandboxes+"/", ac.Protect(ac.SandboxResolver, hostedMux))

	opMux := http.NewServeMux()
	controller.RegisterOperatorHandlers(opMux)
	mux.Handle(fleet.PathPushPolicy, ac.Protect(ac.OperatorResolver, opMux))
	mux.Handle(fleet.PathPushImageGC, ac.Protect(ac.OperatorResolver, opMux))
	mux.Handle(fleet.PathResults, ac.Protect(ac.OperatorResolver, opMux))
}

// parsePairs parses a comma-separated token=subject list into a map.
func parsePairs(s string) map[string]string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	out := make(map[string]string)
	for _, p := range strings.Split(s, ",") {
		tok, subj, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && tok != "" && subj != "" {
			out[tok] = subj
		}
	}
	return out
}

// splitKeys parses a comma-separated API-key list, trimming blanks. An empty
// input yields no keys, which leaves the hosted API open.
func splitKeys(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if k := strings.TrimSpace(p); k != "" {
			out = append(out, k)
		}
	}
	return out
}
