package coved

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// genSelfSigned returns a fresh self-signed leaf cert (valid for 127.0.0.1 and
// localhost) plus its PEM encoding for use as a CA bundle.
func genSelfSigned(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, certPEM
}

// startTLSServer starts an httptest server presenting the given certificate.
func startTLSServer(t *testing.T, cert tls.Certificate) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// writePEM writes data to a temp file and returns its path.
func writePEM(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write pem: %v", err)
	}
	return path
}

func TestWorkerTLSConfig(t *testing.T) {
	dir := t.TempDir()
	_, certPEM := genSelfSigned(t)
	caPath := writePEM(t, certPEM)

	tests := []struct {
		name    string
		cfg     WorkerConfig
		wantNil bool
		wantErr bool
		wantCA  bool
	}{
		{name: "no tls fields uses default transport", cfg: WorkerConfig{}, wantNil: true},
		{name: "ca only sets root pool", cfg: WorkerConfig{TLSClientCA: caPath}, wantCA: true},
		{name: "missing ca file errors", cfg: WorkerConfig{TLSClientCA: filepath.Join(dir, "nope.pem")}, wantErr: true},
		{name: "cert without key errors", cfg: WorkerConfig{TLSClientCertFile: caPath}, wantErr: true},
		{name: "key without cert errors", cfg: WorkerConfig{TLSClientKeyFile: caPath}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := workerTLSConfig(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("workerTLSConfig() = nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("workerTLSConfig() error = %v", err)
			}
			if tt.wantNil {
				if cfg != nil {
					t.Fatalf("workerTLSConfig() = %v, want nil", cfg)
				}
				return
			}
			if cfg == nil {
				t.Fatal("workerTLSConfig() = nil, want config")
			}
			if cfg.MinVersion != tls.VersionTLS12 {
				t.Errorf("MinVersion = %x, want %x", cfg.MinVersion, tls.VersionTLS12)
			}
			if cfg.InsecureSkipVerify {
				t.Error("InsecureSkipVerify = true, want false")
			}
			if tt.wantCA && cfg.RootCAs == nil {
				t.Error("RootCAs = nil, want a pool when a CA is set")
			}
		})
	}
}

// TestWorkerClientTrustsConfiguredCA proves the constructed client trusts the
// configured CA and rejects a server whose cert is signed by a different CA.
func TestWorkerClientTrustsConfiguredCA(t *testing.T) {
	serverCert, serverCertPEM := genSelfSigned(t)
	srv := startTLSServer(t, serverCert)

	// A worker trusting the server's own cert as a CA must connect.
	wTrust, err := NewWorker(WorkerConfig{
		ControllerURL: srv.URL,
		Handler:       stubHandler{},
		TLSClientCA:   writePEM(t, serverCertPEM),
	})
	if err != nil {
		t.Fatalf("NewWorker (trusted): %v", err)
	}
	resp, err := wTrust.client.Get(srv.URL)
	if err != nil {
		t.Fatalf("trusted CA request failed: %v", err)
	}
	resp.Body.Close()

	// A worker pointed at an unrelated CA (a second, independent self-signed
	// cert) must reject the server with a verification error.
	_, otherCertPEM := genSelfSigned(t)
	wReject, err := NewWorker(WorkerConfig{
		ControllerURL: srv.URL,
		Handler:       stubHandler{},
		TLSClientCA:   writePEM(t, otherCertPEM),
	})
	if err != nil {
		t.Fatalf("NewWorker (mismatched): %v", err)
	}
	if _, err := wReject.client.Get(srv.URL); err == nil {
		t.Fatal("request with mismatched CA succeeded, want verification failure")
	}
}

// stubHandler satisfies AssignmentHandler for construction in tests.
type stubHandler struct{}

func (stubHandler) Handle(ctx context.Context, a fleetproto.Assignment) (state, detail string, err error) {
	return "", "", nil
}
