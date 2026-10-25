package main

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
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 8443 serves the pages while the API keeps a web certificate, and stops when it is
// removed (story 35.16 AC15).
func TestHTTPSFollowsTheWebCertificate(t *testing.T) {
	dir := t.TempDir()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "page") })
	go serveHTTPS(ctx, h, []string{dir}, addr, 20*time.Millisecond)
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // a test's own certificate
	get := func() error {
		resp, err := client.Get("https://" + addr + "/")
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	time.Sleep(100 * time.Millisecond)
	if get() == nil {
		t.Fatal("8443 answered without a web certificate")
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "web"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	kder, _ := x509.MarshalECPrivateKey(key)
	_ = os.WriteFile(filepath.Join(dir, webKeyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600)
	_ = os.WriteFile(filepath.Join(dir, webCertFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	deadline := time.Now().Add(3 * time.Second)
	for get() != nil {
		if time.Now().After(deadline) {
			t.Fatal("8443 did not start with the web certificate")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = os.Remove(filepath.Join(dir, webCertFile))
	deadline = time.Now().Add(3 * time.Second)
	for get() == nil {
		if time.Now().After(deadline) {
			t.Fatal("8443 kept serving without the web certificate")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
