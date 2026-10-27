package cluster

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// HomeDatabaseCAFile is the home's database CA beside node.json (the server's name for
// its pinned copy).
const HomeDatabaseCAFile = "home-database-ca.crt"

// HomeDSN is the node's connection to its home's database as the node's other roles make
// it (the server's supervisor.HomeDSN, which the supervisor hands this process as
// DATABASE_URL): node.json's database CA verified (verify-ca, the CA written beside
// node.json), the machine's cluster-link certificate offered (cluster-node.crt and
// cluster-node.key beside node.json); plain only on the home's word that its database has
// no TLS; node.json's URL as it is while neither is known. A home that requires its
// machines' certificates (clientcert=verify-full once the cluster link is required) refused
// the join's URL (sslmode=prefer): TLS without the certificate, then, with pgx's fallback,
// plain text. pgx connects a verify-ca or require DSN over TLS or not at all.
func (s NodeSettings) HomeDSN() (string, error) {
	u, err := url.Parse(s.DatabaseURL)
	if err != nil || u.Host == "" {
		return s.DatabaseURL, fmt.Errorf("node config: the home's database url: %v", err)
	}
	dir := filepath.Dir(s.ConfigPath)
	q := u.Query()
	switch {
	case strings.TrimSpace(s.DatabaseCA) != "":
		path := filepath.Join(dir, HomeDatabaseCAFile)
		if err := pinCA(path, s.DatabaseCA); err != nil {
			return s.DatabaseURL, err
		}
		q.Set("sslmode", "verify-ca")
		q.Set("sslrootcert", filepath.ToSlash(path))
	case strings.EqualFold(strings.TrimSpace(s.DatabaseTLS), "off"):
		q.Set("sslmode", "disable")
		q.Del("sslrootcert")
	default:
		return s.DatabaseURL, nil
	}
	cert, key := filepath.Join(dir, "cluster-node.crt"), filepath.Join(dir, "cluster-node.key")
	if q.Get("sslmode") != "disable" && exists(cert) && exists(key) {
		q.Set("sslcert", filepath.ToSlash(cert))
		q.Set("sslkey", filepath.ToSlash(key))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// pinCA writes a CA certificate (0600) unless the file holds it already; anything but a
// certificate is refused.
func pinCA(path, pemText string) error {
	blk, _ := pem.Decode([]byte(pemText))
	if blk == nil || blk.Type != "CERTIFICATE" {
		return errors.New("node config: the home's database CA is not a certificate")
	}
	if _, err := x509.ParseCertificate(blk.Bytes); err != nil {
		return fmt.Errorf("node config: the home's database CA: %w", err)
	}
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, []byte(pemText)) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(pemText), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
