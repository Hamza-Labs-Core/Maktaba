//go:build integration

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
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

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/machineagent"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// A home whose Postgres requires its machines' certificates (hostssl … clientcert=
// verify-full, as pgbundle's pg_hba.conf has it once the cluster link is required, story
// 35.16), on a real Postgres: the node's roles (supervisor.HomeDSN, as the API and the
// pipeline's supervisor get it) and its check of its login (probeHomeDB) log in with the
// machine's certificate; without it the check fails as TLS, and the machine never asks the
// home for a re-made access however long it lasts (a new password does not mend it: the
// password-rotation loop seen live, 2026-10-26).
func TestANodeLogsInToAHomeThatRequiresItsCertificate(t *testing.T) {
	bin := pgPrograms(t)
	dbCA, clusterCA := newTestCA(t, "home database"), newTestCA(t, "cluster")
	const role = "maktaba_node_6f1c2a3b4d5e4f608a7192b3c4d5e6f7"
	port := startClientCertHome(t, bin, dbCA, clusterCA, role)

	dir := t.TempDir() // the machine: node.json, its keys, its cluster-link certificate
	t.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(dir, "node.json"))
	t.Setenv("MAKTABA_CONFIG", filepath.Join(dir, "server.toml"))
	node, _ := json.Marshal(map[string]any{"node_id": "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7", "name": "pc",
		"private_key": "k", "home": map[string]any{"url": "http://127.0.0.1:8080"},
		"database_url": fmt.Sprintf("postgres://%s:pw@127.0.0.1:%d/maktaba?sslmode=prefer", role, port),
		"database_ca":  dbCA.pem})
	if err := os.WriteFile(filepath.Join(dir, "node.json"), node, 0o600); err != nil {
		t.Fatal(err)
	}
	cert, key := clusterCA.leaf(t, role, false)
	writeCert := func() {
		for name, b := range map[string]string{"cluster-node.crt": cert, "cluster-node.key": key} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(b), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeCert()
	ctx := context.Background()

	// the roles' connection (the API's DATABASE_URL): the certificate, its CN the role
	n := supervisor.NodeConfig()
	db, err := sql.Open("postgres", supervisor.HomeDSN(n))
	if err != nil {
		t.Fatal(err)
	}
	var user, dn string
	var onTLS bool
	err = db.QueryRowContext(ctx, `SELECT current_user::text, s.ssl, COALESCE(s.client_dn, '') FROM pg_stat_ssl s
	    WHERE s.pid = pg_backend_pid()`).Scan(&user, &onTLS, &dn)
	_ = db.Close()
	if err != nil || user != role || !onTLS || !strings.HasSuffix(dn, "CN="+role) {
		t.Fatalf("the roles' login: %q %v %q %v", user, onTLS, dn, err)
	}
	// the check: as the roles
	if err := probeHomeDB(ctx); err != nil {
		t.Fatalf("the check: %v", err)
	}

	// without the certificate: TLS, never asked for again
	for _, name := range []string{"cluster-node.crt", "cluster-node.key"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	err = probeHomeDB(ctx)
	if err == nil || classifyHomeDB(err) != "tls" {
		t.Fatalf("the check without the certificate: %v (%s)", err, classifyHomeDB(err))
	}
	now := time.Now()
	asked := 0
	w := &homeDBWatch{probe: probeHomeDB, clock: func() time.Time { return now },
		target: func() string { return homeDBTarget(supervisor.NodeConfig()) },
		ask:    func(machineagent.HomeDB) bool { asked++; return true }}
	for range 6 {
		now = now.Add(time.Hour)
		w.check(ctx)
		w.maybeAsk()
	}
	if st := w.State(); st.OK || st.Code != "tls" || asked != 0 {
		t.Fatalf("the watch: %+v, %d requests for a re-made access", st, asked)
	}
	// a certificate for another role (a machine that joined under an older role name):
	// Postgres says the password failed; it is cert_mismatch, never asked for either
	cert, key = clusterCA.leaf(t, role+"0", false)
	writeCert()
	err = probeHomeDB(ctx)
	if err == nil || classifyHomeDB(err) != "cert_mismatch" {
		t.Fatalf("another role's certificate: %v (%s)", err, classifyHomeDB(err))
	}
	for range 6 {
		now = now.Add(time.Hour)
		w.check(ctx)
		w.maybeAsk()
	}
	if st := w.State(); st.Code != "cert_mismatch" || asked != 0 {
		t.Fatalf("the watch: %+v, %d requests", st, asked)
	}
	// the certificate back: the check logs in again
	cert, key = clusterCA.leaf(t, role, false)
	writeCert()
	w.check(ctx)
	if st := w.State(); !st.OK {
		t.Fatalf("with the certificate again: %+v", st)
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
// (clientcert=verify-full), makes role in that group and the app's database; its port.
func startClientCertHome(t *testing.T, bin string, dbCA, clusterCA *testCA, role string) int {
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
	admin, err := sql.Open("postgres", "postgres://postgres@127.0.0.1:"+strconv.Itoa(port)+"/postgres?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, q := range []string{`CREATE ROLE maktaba_worker NOLOGIN`,
		`CREATE ROLE ` + role + ` LOGIN PASSWORD 'pw' IN ROLE maktaba_worker`,
		`CREATE DATABASE maktaba OWNER ` + role} {
		if _, err := admin.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return port
}

// testCA signs the TLS test's certificates.
type testCA struct {
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
	pem  string
}

func newTestCA(t *testing.T, name string) *testCA {
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
