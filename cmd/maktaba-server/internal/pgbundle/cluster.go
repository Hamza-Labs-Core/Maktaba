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
	"strconv"
	"strings"
	"time"
)

const (
	// CACertFile and caKeyFile are the cluster database CA, in the keys directory.
	CACertFile = "postgres-ca.crt"
	caKeyFile  = "postgres-ca.key"
	// serverCertFile and serverKeyFile are the server's, in the keys directory too:
	// never in the data directory (backups and restores take it whole).
	serverCertFile = "postgres-server.crt"
	serverKeyFile  = "postgres-server.key"
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

func (b Bundle) serverCert() string { return filepath.Join(b.Keys, serverCertFile) }

// ClusterCAFile is the home's cluster CA certificate (Epic 35 story 35.16), which its API
// keeps beside secrets.key: the server verifies the machines' client certificates with
// it (ssl_ca_file), required of them once the cluster link is (Reach.Require).
const ClusterCAFile = "cluster-ca.crt"

// clusterCA is the cluster CA's file when the home has one ("" before its API made it).
func (b Bundle) clusterCA() string {
	p := filepath.Join(b.Keys, ClusterCAFile)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}
func (b Bundle) serverKey() string { return filepath.Join(b.Keys, serverKeyFile) }

// ServerTLSFiles are the server's certificate and key: signed by the database CA, which
// the machines verify their home's database by (verify-ca: any certificate the CA signed
// passes), so a machine that gives the home up keeps neither (Epic 37). A next start
// with TLS makes them again.
func (b Bundle) ServerTLSFiles() []string { return []string{b.serverCert(), b.serverKey()} }

// TLSReady reports whether the server's certificate and key are in place.
func (b Bundle) TLSReady() bool {
	_, e1 := os.Stat(b.serverCert())
	_, e2 := os.Stat(b.serverKey())
	return e1 == nil && e2 == nil
}

// ensureTLS makes the CA once and (re-)issues the server's certificate when it is
// missing, unreadable or near its end.
func (b Bundle) ensureTLS(now time.Time) error {
	caKey, caCert, err := b.ensureCA(now)
	if err != nil {
		return fmt.Errorf("bundled postgres: the database CA: %w", err)
	}
	certPath := b.serverCert()
	if c, err := readCert(certPath); err == nil && c.NotAfter.Sub(now) > serverRenewBefore &&
		c.CheckSignatureFrom(caCert) == nil {
		if _, err := os.Stat(b.serverKey()); err == nil {
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
	if err := writePEM(b.serverKey(), "PRIVATE KEY", keyDER); err != nil {
		return err
	}
	return writePEM(certPath, "CERTIFICATE", der)
}

// ensureCA is the database CA: made once, when neither its key nor its certificate is
// there. Every machine pins it, so it is never replaced on its own: a key or a
// certificate that is missing beside the other, does not read, or does not go with
// the other is an error (restore the keys folder from a backup).
func (b Bundle) ensureCA(now time.Time) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	keyPath, certPath := filepath.Join(b.Keys, caKeyFile), b.CACert()
	_, keyErr := os.Stat(keyPath)
	_, certErr := os.Stat(certPath)
	if !os.IsNotExist(keyErr) || !os.IsNotExist(certErr) {
		k, err := readKey(keyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("the CA's key %s: %w (it is never made anew while its certificate "+
				"is there: every machine pins it; restore it from a backup)", keyPath, err)
		}
		c, err := readCert(certPath)
		if err != nil {
			return nil, nil, fmt.Errorf("the CA's certificate %s: %w (it is never made anew while its key "+
				"is there: every machine pins it; restore it from a backup)", certPath, err)
		}
		if !k.PublicKey.Equal(c.PublicKey) {
			return nil, nil, fmt.Errorf("the CA's key %s is not its certificate's (%s)", keyPath, certPath)
		}
		return k, c, nil
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
// while a machine may connect (the cluster's Reach), only the machines' roles and their
// copies' replication roles, from the machines' networks (MachinesFrom) and, with the
// default (samenet), each approved machine's own address, into the app's database, over
// TLS (plain connections when TLS is off, which a home only does by its own [postgres]
// tls = "off"). There is no `replication` line: a copy is a logical subscription, which
// connects to the database itself; a physical one (the whole cluster, every row and
// key) is never let in.
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
	if r := b.LastReach(); b.LAN && r.Open {
		frozen := b.Frozen()
		if frozen {
			s.WriteString("# Frozen for a switch of the home: the machines' database roles wait; their copies follow.\n")
		} else {
			s.WriteString("# The cluster's machines: their database roles, and their copies' replication roles.\n")
		}
		from := b.machinesFrom()
		if len(from) == 1 && from[0] == "samenet" {
			// a machine on another network (a VPN) by its own address
			from = append(from, r.machineAddrs()...)
		}
		// the cluster link required (story 35.16 AC12): a machine's role also needs its
		// certificate from the cluster CA, whose CN is that role (a stolen password alone
		// is useless)
		auth := "scram-sha-256"
		if r.Require && b.TLS && b.clusterCA() != "" {
			auth = "scram-sha-256 clientcert=verify-full"
		}
		for _, from := range from {
			if !frozen {
				fmt.Fprintf(&s, "%-7s %-12s %-16s %-15s %s\n", remote, AppDatabase, "+"+WorkerGroup, from, auth)
			}
			fmt.Fprintf(&s, "%-7s %-12s %-16s %-15s scram-sha-256\n", remote, AppDatabase, "+"+ReplicaGroup, from)
		}
	}
	return s.String()
}

// machinesFrom is where the machines may connect from ([postgres] machines_from):
// "samenet" (default: the networks this machine is on, and each approved machine's own
// address), "all", or networks (CIDR, comma-separated: those only; one that does not
// parse is left out, and with none left, samenet).
func (b Bundle) machinesFrom() []string {
	v := strings.TrimSpace(b.MachinesFrom)
	switch strings.ToLower(v) {
	case "", "samenet":
		return []string{"samenet"}
	case "all":
		return []string{"all"}
	}
	var out []string
	for _, c := range strings.Split(v, ",") {
		if _, n, err := net.ParseCIDR(strings.TrimSpace(c)); err == nil {
			out = append(out, n.String())
		}
	}
	if len(out) == 0 {
		return []string{"samenet"}
	}
	return out
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
// their groups, which it then administers), their replication roles and slots, read
// the replication's progress, and end a revoked machine's sessions at once; and it
// makes the replicas' group (each machine's replication role joins it; it reads the
// tables published to the machines: specs/epics/37-node-replica), administered by the
// app role without the app role taking its rights; and it lets the app role keep
// Postgres's own cap on what a slot holds (max_slot_wal_keep_size) at its rule
// (rules.cluster.replica_wal_keep_gb), so the cap holds with the API down too. The SQL
// functions that make, read, move or drop a slot are the app role's alone in the app's
// database: a machine's replication role follows its publication over the replication
// protocol and needs none of them (with one it could read the slots' changes past its
// publication). Prepare runs it once per clusterRev (the marker), in the app's database
// (a function's grant is that database's); every statement is safe to run again. On a
// server older than Maktaba's own Postgres 16 what it lacks is left out: a parameter's
// grant (15), a membership without its rights (16).
const clusterSQL = `\set ON_ERROR_STOP on
\c ` + AppDatabase + `
ALTER ROLE ` + AppRole + ` WITH CREATEROLE REPLICATION;
GRANT pg_monitor TO ` + AppRole + `;
GRANT pg_signal_backend TO ` + AppRole + `;
DO $$
BEGIN
    IF current_setting('server_version_num')::int >= 150000 THEN
        EXECUTE 'GRANT ALTER SYSTEM ON PARAMETER max_slot_wal_keep_size TO ` + AppRole + `';
    END IF;
END
$$;
GRANT EXECUTE ON FUNCTION pg_reload_conf() TO ` + AppRole + `;
DO $$
DECLARE f regprocedure;
BEGIN
    FOR f IN SELECT p.oid::regprocedure FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
              WHERE n.nspname = 'pg_catalog' AND p.proname IN ('pg_create_logical_replication_slot',
                    'pg_create_physical_replication_slot', 'pg_copy_logical_replication_slot',
                    'pg_copy_physical_replication_slot', 'pg_logical_slot_get_changes',
                    'pg_logical_slot_get_binary_changes', 'pg_logical_slot_peek_changes',
                    'pg_logical_slot_peek_binary_changes', 'pg_replication_slot_advance',
                    'pg_drop_replication_slot') LOOP
        EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
        EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO ` + AppRole + `', f);
    END LOOP;
END
$$;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + ReplicaGroup + `') THEN
        CREATE ROLE ` + ReplicaGroup + ` NOLOGIN;
    END IF;
    IF current_setting('server_version_num')::int >= 160000 THEN
        EXECUTE 'GRANT ` + ReplicaGroup + ` TO ` + AppRole + ` WITH ADMIN OPTION, INHERIT FALSE, SET FALSE';
    ELSE
        EXECUTE 'GRANT ` + ReplicaGroup + ` TO ` + AppRole + ` WITH ADMIN OPTION';
    END IF;
END
$$;
`

// clusterRev is clusterSQL's revision: a cluster whose marker names an older one (or
// none: the first) runs it again.
const clusterRev = 2

// clusterDone reports whether the cluster bootstrap ran at clusterRev here.
func (b Bundle) clusterDone() bool {
	raw, err := os.ReadFile(filepath.Join(b.Data, clusterMarker))
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "rev "); ok {
			n, err := strconv.Atoi(v)
			return err == nil && n >= clusterRev
		}
	}
	return false
}
