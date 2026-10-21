package pgbundle

// Serving the cluster's machines (feat/node-replica): a home's bundled Postgres is
// what every joined machine's database role and its replica connect to, so it
// listens on the LAN, over TLS with a certificate of its own CA (made here, the
// nodes trust only it), and its pg_hba.conf — written by Maktaba at every start —
// lets in from off this machine only the machines' roles (the maktaba_worker group)
// and their replication roles (maktaba_replicas), with scram-sha-256 passwords.
// The app role can create those roles, their replication slots and the publication
// (CREATEROLE, REPLICATION, pg_monitor), granted once by the cluster bootstrap.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// CACertFile and caKeyFile are the cluster database CA, in the keys directory.
	CACertFile = "postgres-ca.crt"
	caKeyFile  = "postgres-ca.key"
	// serverCertFile and serverKeyFile are the server's, in the data directory
	// (postgres's default names there).
	serverCertFile = "server.crt"
	serverKeyFile  = "server.key"
	// clusterMarker records that the cluster bootstrap ran.
	clusterMarker = "maktaba-bootstrap-cluster.done"
	// WorkerGroup and ReplicaGroup are the machines' role groups.
	WorkerGroup  = "maktaba_worker"
	ReplicaGroup = "maktaba_replicas"
)

// caValidity and serverValidity: the CA outlives every release; the server's
// certificate is re-issued at a start within serverRenewBefore of its end.
var (
	caValidity        = 20 * 365 * 24 * time.Hour
	serverValidity    = 2 * 365 * 24 * time.Hour
	serverRenewBefore = 60 * 24 * time.Hour
)

// CACert is the path of the CA certificate the machines verify this server with.
func (b Bundle) CACert() string { return filepath.Join(b.Keys, CACertFile) }

// TLSReady reports whether the server's certificate and key are in place.
func (b Bundle) TLSReady() bool {
	_, e1 := os.Stat(filepath.Join(b.Data, serverCertFile))
	_, e2 := os.Stat(filepath.Join(b.Data, serverKeyFile))
	return e1 == nil && e2 == nil
}

// ensureTLS makes the CA once and (re-)issues the server's certificate when it is
// missing, unreadable or near its end.
func (b Bundle) ensureTLS(now time.Time) error {
	caKey, caCert, err := b.ensureCA(now)
	if err != nil {
		return fmt.Errorf("bundled postgres: the database CA: %w", err)
	}
	certPath := filepath.Join(b.Data, serverCertFile)
	if c, err := readCert(certPath); err == nil && c.NotAfter.Sub(now) > serverRenewBefore &&
		c.CheckSignatureFrom(caCert) == nil {
		if _, err := os.Stat(filepath.Join(b.Data, serverKeyFile)); err == nil {
			return nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "Maktaba home database", Organization: []string{"Maktaba"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames(host),
		IPAddresses:  localIPs(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	// postgres refuses a key others may read (0600, its owner)
	if err := writePEM(filepath.Join(b.Data, serverKeyFile), "PRIVATE KEY", keyDER); err != nil {
		return err
	}
	return writePEM(certPath, "CERTIFICATE", der)
}

func (b Bundle) ensureCA(now time.Time) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	keyPath, certPath := filepath.Join(b.Keys, caKeyFile), b.CACert()
	if k, err := readKey(keyPath); err == nil {
		if c, err := readCert(certPath); err == nil {
			return k, c, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Maktaba cluster database CA", Organization: []string{"Maktaba"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePEM(keyPath, "PRIVATE KEY", keyDER); err != nil {
		return nil, nil, err
	}
	if err := writePEM(certPath, "CERTIFICATE", der); err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

func dnsNames(host string) []string {
	names := []string{"localhost"}
	if host = strings.TrimSpace(host); host != "" {
		names = append(names, host)
		if !strings.Contains(host, ".") {
			names = append(names, host+".local")
		}
	}
	return names
}

func localIPs() []net.IP {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

func writePEM(path, kind string, der []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readCert(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(raw)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM certificate")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func readKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(raw)
	if blk == nil {
		return nil, errors.New("not a PEM key")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an ECDSA key")
	}
	return ek, nil
}

// hbaConf is the managed pg_hba.conf: this machine with passwords; from off it, only
// the machines' roles and their replication roles, over TLS (plain connections when
// TLS is off, which a home only does by its own [postgres] tls = "off").
func (b Bundle) hbaConf() string {
	remote := "hostssl"
	if !b.TLS {
		remote = "host"
	}
	var s strings.Builder
	s.WriteString("# Managed by Maktaba (pgbundle): written at every start, edits here are lost.\n")
	s.WriteString("# This machine: every role, with its password.\n")
	s.WriteString("local   all          all                              scram-sha-256\n")
	s.WriteString("host    all          all              127.0.0.1/32    scram-sha-256\n")
	s.WriteString("host    all          all              ::1/128         scram-sha-256\n")
	if b.LAN {
		s.WriteString("# The cluster's machines: their database roles, and the replicas of their copy.\n")
		fmt.Fprintf(&s, "%-7s %-12s %-16s %-15s scram-sha-256\n", remote, AppDatabase, "+"+WorkerGroup, "all")
		fmt.Fprintf(&s, "%-7s %-12s %-16s %-15s scram-sha-256\n", remote, AppDatabase, "+"+ReplicaGroup, "all")
		fmt.Fprintf(&s, "%-7s %-12s %-16s %-15s scram-sha-256\n", remote, "replication", "+"+ReplicaGroup, "all")
	}
	return s.String()
}

func (b Bundle) writeHBA() error {
	path := filepath.Join(b.Data, "pg_hba.conf")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.hbaConf()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// clusterSQL lets the app role provision the machines: create their roles (and
// their groups, which it then administers), their replication roles and slots, and
// read the replication's progress (Prepare runs it once, the marker). Every
// statement is safe to run again.
const clusterSQL = `\set ON_ERROR_STOP on
ALTER ROLE ` + AppRole + ` WITH CREATEROLE REPLICATION;
GRANT pg_monitor TO ` + AppRole + `;
`
