package httpapi_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/httpapi"
	"github.com/zlrrr/multi-agent-system-turbo/internal/service"
	"github.com/zlrrr/multi-agent-system-turbo/internal/store"
)

// selfSigned writes a certificate and key for 127.0.0.1, and returns the paths
// and a pool that trusts it.
func selfSigned(t *testing.T) (certPath, keyPath string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mas-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	pool = x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("the generated certificate could not be trusted")
	}
	return certPath, keyPath, pool
}

// freePort returns a port nothing is listening on. There is a race between
// closing and re-binding, which is unavoidable without a Serve that reports its
// listener — and losing it produces a bind error, not a wrong result.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// TestServesTLSDirectly is FR-010. Not every deployment has an ingress in front
// of it, and telling those operators to install one before they can have
// authentication would be answering a different question.
func TestServesTLSDirectly(t *testing.T) {
	certPath, keyPath, pool := selfSigned(t)
	port := freePort(t)

	promURL, lokiURL := stubTelemetry(t)
	cfg := config.Default()
	cfg.Log.Level = "error"
	cfg.Store = config.StoreConfig{Type: "memory"}
	cfg.Source.Enabled = false
	cfg.LLM = config.LLMConfig{Provider: "mock", Model: "mock-1", MaxTokens: 512}
	cfg.Telemetry.Metrics = []config.MetricsSource{{
		Name: "primary", Type: "prometheus", URL: promURL,
		Timeout: config.Duration(2 * time.Second), MaxSamples: 100,
	}}
	cfg.Telemetry.Logs = []config.LogsSource{{
		Name: "primary", Type: "loki", URL: lokiURL,
		Timeout: config.Duration(2 * time.Second), MaxLines: 100,
	}}
	cfg.Targets = []config.TargetConfig{{ID: "redis-prod", Kind: "redis", Version: "7.2.4"}}
	cfg.Server.Addr = fmt.Sprintf("127.0.0.1:%d", port)
	cfg.Server.TLS = config.ServerTLS{CertFile: certPath, KeyFile: keyPath}
	cfg.Server.Auth = config.ServerAuth{Tokens: []config.APIToken{
		{Name: "oncall", Token: config.Secret(oncallToken), Scopes: []string{"read"}},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	svc, err := service.New(service.Options{Config: cfg, Store: store.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- httpapi.Serve(ctx, svc) }()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	url := fmt.Sprintf("https://127.0.0.1:%d/healthz", port)

	var last error
	for i := 0; i < 50; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.TLS == nil {
				t.Fatal("the connection was not TLS")
			}
			if resp.TLS.Version < tls.VersionTLS12 {
				t.Errorf("negotiated TLS %x, below the 1.2 minimum", resp.TLS.Version)
			}
			cancel()
			<-done
			return
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("the server never answered over TLS: %v", last)
}
