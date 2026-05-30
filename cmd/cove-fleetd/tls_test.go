// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTestCert generates a self-signed ECDSA cert/key pair under dir and
// returns their PEM paths. It is used to exercise the TLS file-loading paths
// without committing any cert material.
func writeTestCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

func TestBuildServerTLSConfig(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCert(t, dir)
	// Reuse the self-signed cert as a client CA bundle for the mTLS path.
	caPath := certPath

	tests := []struct {
		name     string
		cert     string
		key      string
		clientCA string
		wantNil  bool
		wantErr  bool
		wantMTLS bool
	}{
		{name: "neither set serves plaintext", wantNil: true},
		{name: "only cert set is rejected", cert: certPath, wantErr: true},
		{name: "only key set is rejected", key: keyPath, wantErr: true},
		{name: "both set enables tls", cert: certPath, key: keyPath},
		{name: "client ca enables mtls", cert: certPath, key: keyPath, clientCA: caPath, wantMTLS: true},
		{name: "missing cert file errors", cert: filepath.Join(dir, "nope.pem"), key: keyPath, wantErr: true},
		{name: "missing client ca errors", cert: certPath, key: keyPath, clientCA: filepath.Join(dir, "nope.pem"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := buildServerTLSConfig(tt.cert, tt.key, tt.clientCA)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("buildServerTLSConfig() = nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildServerTLSConfig() error = %v", err)
			}
			if tt.wantNil {
				if cfg != nil {
					t.Fatalf("buildServerTLSConfig() = %v, want nil", cfg)
				}
				return
			}
			if cfg == nil {
				t.Fatal("buildServerTLSConfig() = nil, want config")
			}
			if cfg.MinVersion != tls.VersionTLS12 {
				t.Errorf("MinVersion = %x, want %x", cfg.MinVersion, tls.VersionTLS12)
			}
			if len(cfg.Certificates) != 1 {
				t.Errorf("Certificates = %d, want 1", len(cfg.Certificates))
			}
			gotMTLS := cfg.ClientAuth == tls.RequireAndVerifyClientCert
			if gotMTLS != tt.wantMTLS {
				t.Errorf("mTLS = %v, want %v", gotMTLS, tt.wantMTLS)
			}
			if tt.wantMTLS && cfg.ClientCAs == nil {
				t.Error("ClientCAs = nil, want a pool when mTLS is on")
			}
		})
	}
}
