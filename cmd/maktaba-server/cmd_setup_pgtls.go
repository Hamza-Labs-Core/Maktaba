package main

// maktaba-server setup --pg-tls (Epic 35 story 35.16 AC12, design §18.8): the home's
// Postgres verifies the machines' client certificates from the cluster CA, required of
// their roles once the cluster link is (a stolen password alone is then useless).
//
// The Postgres Maktaba runs (the bundled one) does it by itself: it is started with the
// cluster CA (ssl_ca_file) and its pg_hba.conf asks the machines' roles for their
// certificate once the link is required. For a Postgres another admin runs (EC5) this
// command issues its server certificate from the cluster CA and prints the settings to
// apply, rather than touching a server it does not own.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// PG server certificate files the command writes in the keys directory.
const (
	pgServerCertFile = "cluster-pg-server.crt"
	pgServerKeyFile  = "cluster-pg-server.key"
)

// runSetupPGTLS issues the operator Postgres' server certificate (or says the bundled
// one needs nothing) and prints what to apply.
func runSetupPGTLS(out io.Writer, cfg config.Config, keysDir string, sans []string, now time.Time) error {
	if _, ok, err := pgbundle.Resolve(cfg, bundledExe()); err == nil && ok {
		fmt.Fprintln(out, "This home runs its own Postgres: it already verifies the machines' certificates from the")
		fmt.Fprintln(out, "cluster CA, and asks for them once the cluster link is required. Nothing to do.")
		return nil
	}
	caKey, caCert, err := loadClusterCA(keysDir)
	if err != nil {
		return err
	}
	certPEM, keyPEM, err := issuePGServer(caKey, caCert, sans, now)
	if err != nil {
		return err
	}
	certPath, keyPath := filepath.Join(keysDir, pgServerCertFile), filepath.Join(keysDir, pgServerKeyFile)
	for _, f := range []struct {
		path string
		b    []byte
	}{{certPath, certPEM}, {keyPath, keyPEM}} {
		if err := os.WriteFile(f.path, f.b, 0o600); err != nil {
			return err
		}
	}
	caPath := filepath.Join(keysDir, pgbundle.ClusterCAFile)
	fmt.Fprintf(out, "The home's Postgres is not one Maktaba runs: apply these settings there (copy the files to\n")
	fmt.Fprintf(out, "the database server; the key stays readable by the postgres user only).\n\n")
	fmt.Fprintf(out, "postgresql.conf:\n  ssl = on\n  ssl_cert_file = '%s'\n  ssl_key_file = '%s'\n  ssl_ca_file = '%s'\n\n",
		filepath.ToSlash(certPath), filepath.ToSlash(keyPath), filepath.ToSlash(caPath))
	fmt.Fprintln(out, "pg_hba.conf, once the cluster link is required: the role of each machine whose processes present")
	fmt.Fprintln(out, "their certificate (the Machines page says which do not yet), on its own line before the group's:")
	fmt.Fprintln(out, "  hostssl  <database>  <that machine's role>  <the machines' networks>  scram-sha-256 clientcert=verify-full")
	fmt.Fprintln(out, "  hostssl  <database>  +maktaba_worker        <the machines' networks>  scram-sha-256")
	fmt.Fprintln(out, "Never ask it of the whole group: a machine whose processes do not present one (an older release)")
	fmt.Fprintln(out, "would be cut off its database.")
	fmt.Fprintln(out, "\nThen reload the server (SELECT pg_reload_conf();). The machines connect with their certificates already.")
	return nil
}

// bundledExe is this binary (where the bundled Postgres is looked for), "" when unknown.
func bundledExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// loadClusterCA reads the home's cluster CA (its API keeps it beside secrets.key).
func loadClusterCA(dir string) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	keyPEM, err1 := os.ReadFile(filepath.Join(dir, "cluster-ca.key"))        //nolint:gosec // the home's own keys directory
	certPEM, err2 := os.ReadFile(filepath.Join(dir, pgbundle.ClusterCAFile)) //nolint:gosec // the home's own keys directory
	if err1 != nil || err2 != nil {
		return nil, nil, errors.New("this machine has no cluster CA: run it on the home, after its API has started once")
	}
	kb, _ := pem.Decode(keyPEM)
	cb, _ := pem.Decode(certPEM)
	if kb == nil || cb == nil {
		return nil, nil, errors.New("the cluster CA's files are not PEM")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("the cluster CA's key: %w", err)
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil || !cert.IsCA {
		return nil, nil, errors.New("the cluster CA's certificate is not a CA")
	}
	return key, cert, nil
}

// issuePGServer signs the home Postgres' server certificate (its addresses, a year).
func issuePGServer(caKey *ecdsa.PrivateKey, ca *x509.Certificate, sans []string, now time.Time) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "maktaba-home-pg_server"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(365 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true}
	for _, s := range sans {
		s = strings.Trim(strings.TrimSpace(s), "[]")
		if ip := net.ParseIP(s); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if s != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, s)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), nil
}

// pgSANs are the names the home's database is reached at: this machine's host names and
// addresses, and the host of its database url.
func pgSANs(cfg config.Config) []string {
	out := []string{"localhost", "127.0.0.1", "::1"}
	if h, err := os.Hostname(); err == nil && h != "" {
		out = append(out, h, strings.SplitN(h, ".", 2)[0]+".local")
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, ipn.IP.String())
			}
		}
	}
	if u := cfg.Database.URL; u != "" {
		if i := strings.LastIndex(u, "@"); i >= 0 {
			host := u[i+1:]
			host = strings.SplitN(host, "/", 2)[0]
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			out = append(out, host)
		}
	}
	return out
}
