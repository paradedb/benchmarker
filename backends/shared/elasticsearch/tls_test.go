package elasticsearch

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRejectsUntrustedTLSCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	backend, err := New(server.URL, DriverConfig{})
	if err != nil {
		t.Fatal(err)
	}
	driver := backend.(*Driver)
	resp, err := driver.client.Get(server.URL)
	if err == nil {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close failed: %v", err)
		}
		t.Fatal("accepted an untrusted TLS certificate")
	}
	var unknownCA x509.UnknownAuthorityError
	if !errors.As(err, &unknownCA) {
		t.Fatalf("expected certificate verification error, got %v", err)
	}
	// Trust the test CA explicitly and verify that HTTPS then succeeds.
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	driver.client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	resp, err = driver.client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close failed: %v", err)
	}
}
