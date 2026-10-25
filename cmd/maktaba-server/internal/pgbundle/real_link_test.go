//go:build integration

package pgbundle

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// linkPGHome is the Postgres install the link's test runs (MAKTABA_TEST_PG_HOME, else
// one on PATH or Debian/Ubuntu's).
func linkPGHome(t *testing.T) string {
	t.Helper()
	if h := os.Getenv("MAKTABA_TEST_PG_HOME"); h != "" {
		return h
	}
	if p, err := exec.LookPath("postgres"); err == nil {
		return filepath.Dir(filepath.Dir(p))
	}
	if dirs, _ := filepath.Glob("/usr/lib/postgresql/*/bin/postgres"); len(dirs) > 0 {
		return filepath.Dir(filepath.Dir(dirs[len(dirs)-1]))
	}
	t.Skip("no postgres on this machine")
	return ""
}

// lanIP is one of this machine's non-loopback IPv4 addresses (the machines' path).
func lanIP(t *testing.T) string {
	t.Helper()
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
				return ipn.IP.String()
			}
		}
	}
	t.Skip("no LAN address on this machine")
	return ""
}

// TestRealPostgresClientCertificates (story 35.16 TC9): once the cluster link is
// required, a machine's role logs in to the home's Postgres only with its certificate
// from the cluster CA, whose CN is that role: a stolen password alone, or another
// machine's certificate, is refused.
func TestRealPostgresClientCertificates(t *testing.T) {
	home := linkPGHome(t)
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	ip := lanIP(t)
	port := 0
	if p, err := strconv.Atoi(os.Getenv("MAKTABA_TEST_PG_PORT")); err == nil && p > 0 {
		port = p
	} else {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port = ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
	}
	root, err := os.MkdirTemp("", "mkln") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	keys := filepath.Join(root, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	// the home's cluster CA, as its API keeps it beside secrets.key
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cluster CA"}, IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(24 * time.Hour)}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	if err := os.WriteFile(filepath.Join(keys, ClusterCAFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	clientCert := func(cn string) (string, string) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
			NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		kder, _ := x509.MarshalECPrivateKey(key)
		cp, kp := filepath.Join(root, cn+".crt"), filepath.Join(root, cn+".key")
		_ = os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
		_ = os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600)
		return cp, kp
	}

	b := Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: keys, Backups: filepath.Join(root, "backups"),
		Port: port, LAN: true, TLS: true, MachinesFrom: ip + "/32"}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	// a machine is approved and the link is required: the reach the watcher would keep
	if err := b.KeepReach(Reach{Open: true, Require: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.writeHBA(); err != nil {
		t.Fatal(err)
	}
	args := b.ServerArgs()
	for i, a := range args {
		if a == "listen_addresses=*" {
			args[i] = "listen_addresses=127.0.0.1," + ip // the test listens on what it needs only
		}
	}
	srv, err := b.serverCommand(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })
	deadline := time.Now().Add(30 * time.Second)
	for Ready(ctx, b.Addr(), AppRole, AppDatabase) != nil {
		if time.Now().After(deadline) {
			t.Fatal("postgres not ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	superURL, err := b.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	super, err := sql.Open("postgres", superURL)
	if err != nil {
		t.Fatal(err)
	}
	defer super.Close()
	const role = "maktaba_node_0b6f3c1e2a4d4e8f9c715d2a8b3e6f10"
	// the machines' group (its migration makes it on a real home)
	if _, err := super.ExecContext(ctx, `DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+WorkerGroup+
		`') THEN CREATE ROLE `+WorkerGroup+` NOLOGIN; END IF; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := super.ExecContext(ctx, `CREATE ROLE `+role+` LOGIN PASSWORD 'node-pass' IN ROLE `+WorkerGroup); err != nil {
		t.Fatal(err)
	}
	login := func(cert, key string) error {
		q := url.Values{"sslmode": {"require"}, "connect_timeout": {"5"}}
		if cert != "" {
			q.Set("sslcert", filepath.ToSlash(cert))
			q.Set("sslkey", filepath.ToSlash(key))
		}
		u := url.URL{Scheme: "postgres", User: url.UserPassword(role, "node-pass"), Host: net.JoinHostPort(ip, strconv.Itoa(port)),
			Path: "/" + AppDatabase, RawQuery: q.Encode()}
		db, err := sql.Open("postgres", u.String())
		if err != nil {
			return err
		}
		defer db.Close()
		return db.PingContext(ctx)
	}
	cert, key := clientCert(role)
	if err := login(cert, key); err != nil {
		t.Fatalf("the machine with its certificate: %v", err)
	}
	if err := login("", ""); err == nil || !strings.Contains(strings.ToLower(err.Error()), "cert") {
		t.Fatalf("a stolen password alone was let in: %v", err)
	}
	other, okey := clientCert("maktaba_node_ffffffffffffffffffffffffffffffff")
	if err := login(other, okey); err == nil {
		t.Fatal("another machine's certificate was let in")
	}
}
