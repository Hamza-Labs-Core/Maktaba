//go:build integration

package cluster

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A node's streaming against a home whose Postgres requires its machines' certificates
// (hostssl … clientcert=verify-full, as the home writes its pg_hba.conf once the cluster
// link is required), on a real Postgres, with pgx: the node's connection (HomeDSN, the
// roles' DATABASE_URL) logs in with the machine's certificate; without it, or against a
// server without TLS, it does not connect at all (never pgx's plain fallback); the join's
// own URL (sslmode=prefer) is refused as it was live ("no encryption").
func TestStreamingLogsInToAHomeThatRequiresItsCertificate(t *testing.T) {
	bin := pgPrograms(t)
	dbCA, clusterCA := newCA(t, "home database"), newCA(t, "cluster")
	const role = "maktaba_node_6f1c2a3b4d5e4f608a7192b3c4d5e6f7"
	h := startClientCertHome(t, bin, dbCA, clusterCA, role)
	dir := t.TempDir()
	s := NodeSettings{DatabaseURL: fmt.Sprintf("postgres://%s:pw@127.0.0.1:%d/maktaba?sslmode=prefer", role, h.port),
		ConfigPath: filepath.Join(dir, "node.json"), DatabaseCA: dbCA.pem}
	writeCert := func(cn string) {
		cert, key := clusterCA.leaf(t, cn, false)
		for name, b := range map[string]string{"cluster-node.crt": cert, "cluster-node.key": key} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(b), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx := context.Background()
	login := func(dsn string) (string, error) {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return "", err
		}
		defer pool.Close()
		var user, dn string
		var onTLS bool
		err = pool.QueryRow(ctx, `SELECT current_user::text, s.ssl, COALESCE(s.client_dn, '') FROM pg_stat_ssl s
		    WHERE s.pid = pg_backend_pid()`).Scan(&user, &onTLS, &dn)
		if err == nil && (!onTLS || !strings.HasSuffix(dn, "CN="+role)) {
			err = fmt.Errorf("logged in as %s without its certificate (tls %v, %q)", user, onTLS, dn)
		}
		return user, err
	}

	writeCert(role)
	dsn, err := s.HomeDSN()
	if err != nil {
		t.Fatal(err)
	}
	if user, err := login(dsn); err != nil || user != role {
		t.Fatalf("with its certificate: %q %v", user, err)
	}
	// without it: refused over TLS, never retried in plain text
	for _, f := range []string{"cluster-node.crt", "cluster-node.key"} {
		_ = os.Remove(filepath.Join(dir, f))
	}
	dsn, _ = s.HomeDSN()
	if _, err := login(dsn); err == nil || !strings.Contains(err.Error(), "certificate") ||
		strings.Contains(err.Error(), "no encryption") {
		t.Fatalf("without its certificate: %v", err)
	}
	// the join's own URL, what a node fell back to: refused as it was live
	if _, err := login(s.DatabaseURL); err == nil || !strings.Contains(err.Error(), "no encryption") {
		t.Fatalf("the join's URL: %v", err)
	}
	// a server without TLS (a line that would let a plain login in): no connection
	writeCert(role)
	h.exec(t, `ALTER SYSTEM SET ssl = off`)
	f, err := os.OpenFile(filepath.Join(h.data, "pg_hba.conf"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("host maktaba +maktaba_worker 127.0.0.1/32 scram-sha-256\n")
	_ = f.Close()
	h.exec(t, `SELECT pg_reload_conf()`)
	dsn, _ = s.HomeDSN()
	if _, err := login(dsn); err == nil {
		t.Fatal("logged in to a server without TLS")
	}
}

type tlsHome struct {
	data string
	port int
}

func (h tlsHome) exec(t *testing.T, q string) {
	t.Helper()
	c, err := pgx.Connect(context.Background(), "postgres://postgres@127.0.0.1:"+strconv.Itoa(h.port)+
		"/postgres?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if _, err := c.Exec(context.Background(), q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// pgPrograms finds Postgres's programs: MAKTABA_TEST_PG_BIN, PATH, or Debian/Ubuntu's
// (GitHub's runners carry them).
func pgPrograms(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("MAKTABA_TEST_PG_BIN"); v != "" {
		return v
	}
	if p, err := exec.LookPath("initdb"); err == nil {
		return filepath.Dir(p)
	}
	if dirs, _ := filepath.Glob("/usr/lib/postgresql/*/bin/initdb"); len(dirs) > 0 {
		return filepath.Dir(dirs[len(dirs)-1])
	}
	t.Skip("no Postgres programs (initdb) on this machine")
	return ""
}

// startClientCertHome starts a Postgres with TLS (its certificate from dbCA) that takes
// the machines' group only over TLS with a certificate from clusterCA whose CN is the role
// (clientcert=verify-full), and makes role in that group and the app's database.
func startClientCertHome(t *testing.T, bin string, dbCA, clusterCA *testCA, role string) tlsHome {
	t.Helper()
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	root, err := os.MkdirTemp("", "mkpgtls") // short: Postgres's paths
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	data := filepath.Join(root, "data")
	if out, err := exec.Command(filepath.Join(bin, "initdb"), "-D", data, "-U", "postgres", "--auth=trust",
		"-E", "UTF8", "--no-instructions").CombinedOutput(); err != nil {
		t.Fatalf("initdb: %v: %s", err, out)
	}
	crt, key := dbCA.leaf(t, "localhost", true)
	files := map[string]string{"server.crt": crt, "server.key": key, "cluster-ca.crt": clusterCA.pem,
		"pg_hba.conf": "hostssl maktaba +maktaba_worker 127.0.0.1/32 scram-sha-256 clientcert=verify-full\n" +
			"host    all     postgres        127.0.0.1/32 trust\n"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(data, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	conf, err := os.OpenFile(filepath.Join(data, "postgresql.conf"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conf, "\nport = %d\nlisten_addresses = '127.0.0.1'\nunix_socket_directories = ''\n"+
		"ssl = on\nssl_cert_file = 'server.crt'\nssl_key_file = 'server.key'\nssl_ca_file = 'cluster-ca.crt'\n", port)
	_ = conf.Close()
	ctl := func(args ...string) {
		// no pipe for its output: the server it starts would hold it
		cmd := exec.Command(filepath.Join(bin, "pg_ctl"), append([]string{"-D", data, "-l",
			filepath.Join(root, "pg.log")}, args...)...)
		if err := cmd.Run(); err != nil {
			b, _ := os.ReadFile(filepath.Join(root, "pg.log"))
			t.Fatalf("pg_ctl %v: %v: %s", args, err, b)
		}
	}
	ctl("-w", "start")
	t.Cleanup(func() { ctl("-m", "immediate", "-w", "stop") })
	h := tlsHome{data: data, port: port}
	for _, q := range []string{`CREATE ROLE maktaba_worker NOLOGIN`,
		`CREATE ROLE ` + role + ` LOGIN PASSWORD 'pw' IN ROLE maktaba_worker`,
		`CREATE DATABASE maktaba OWNER ` + role} {
		h.exec(t, q)
	}
	return h
}

// testCA signs the TLS test's certificates.
type testCA struct {
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
	pem  string
}

func newCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{key: key, cert: cert, pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

// leaf is a certificate the CA signs for cn (a server's for 127.0.0.1 and localhost) and
// its key, PEM.
func (c *testCA) leaf(t *testing.T, cn string, server bool) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames, tmpl.IPAddresses = []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}
