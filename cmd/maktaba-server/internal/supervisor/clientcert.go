package supervisor

import (
	"crypto/x509"
	"encoding/pem"
	"net/url"
	"os"
	"path/filepath"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// PresentsClientCert reports whether this machine's processes present its cluster
// certificate to its home's database: HomeDSN, the connection they log in with, offers it
// (the certificate and its key beside node.json, on a verified TLS connection), and it
// names the role they log in as (its CN: clientcert=verify-full lets a role in only so; a
// machine joined before 2026-10-18 logs in as its 8-hex role while its certificate may
// name the 32-hex one, and must not ask the home for it then). The machine
// says so in its update.state (db_client_cert), and only then does the home, once the
// cluster link is required, ask this machine's role for it (story 35.16): a machine whose
// connection does not offer one keeps its password alone, never cut off its database.
func PresentsClientCert(n config.NodeConfig) bool {
	if !n.Joined() {
		return false
	}
	// the connection the roles use even when its CA could not be pinned (HomeDSN says so)
	dsn, _ := n.HomeDSN(config.KeysDir())
	if dsn == "" {
		return false
	}
	u, err := url.Parse(withClientCert(dsn, nodeConfigDir()))
	if err != nil {
		return false
	}
	q := u.Query()
	if q.Get("sslcert") == "" || q.Get("sslkey") == "" || u.User == nil {
		return false
	}
	return certCN(filepath.FromSlash(q.Get("sslcert"))) == u.User.Username()
}

// certCN is the CN of the certificate in the PEM file at path ("" when it cannot be read).
func certCN(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	b, _ := pem.Decode(raw)
	if b == nil {
		return ""
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		return ""
	}
	return c.Subject.CommonName
}
