package supervisor

import (
	"net/url"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// PresentsClientCert reports whether this machine's processes present its cluster
// certificate to its home's database: HomeDSN, the connection they log in with, offers it
// (the certificate and its key beside node.json, on a verified TLS connection). The machine
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
	return q.Get("sslcert") != "" && q.Get("sslkey") != ""
}
