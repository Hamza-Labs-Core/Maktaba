// Package testpg reads MAKTABA_TEST_PG_URL, the scratch Postgres of the Go real-database
// tests (api, streaming, cmd/maktaba-server, shared/components), so one URL works in
// every suite.
//
// The pipeline's tests (asyncpg) and pgx take a URL without sslmode and fall back to a
// plain connection when the server has no SSL; lib/pq (the api, its `migrate` command,
// the supervisor) treats a missing sslmode as require and fails with "pq: SSL is not
// enabled on the server" (#644). For a server on this machine (localhost, a loopback
// address or a socket directory), Get adds sslmode=disable when the URL sets none. A
// remote server's URL is left as it is: a missing sslmode there is never weakened.
package testpg

import (
	"net"
	"net/url"
	"os"
	"strings"
)

// Env names the variable.
const Env = "MAKTABA_TEST_PG_URL"

// Get is MAKTABA_TEST_PG_URL made usable by lib/pq (Normalize), "" when it is unset.
func Get() string { return Normalize(os.Getenv(Env)) }

// Normalize adds sslmode=disable to a URL or key=value dsn of a server on this machine
// that sets no sslmode; any other dsn comes back unchanged.
func Normalize(dsn string) string {
	if strings.TrimSpace(dsn) == "" {
		return dsn
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		q := u.Query()
		if q.Has("sslmode") {
			return dsn
		}
		host := u.Hostname()
		if h := q.Get("host"); h != "" { // ?host=/var/run/postgresql
			host = h
		}
		if !local(host) {
			return dsn
		}
		q.Set("sslmode", "disable")
		u.RawQuery = q.Encode()
		return u.String()
	}
	host := ""
	for _, f := range strings.Fields(dsn) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "sslmode":
			return dsn
		case "host":
			host = strings.Trim(v, "'")
		}
	}
	if !local(host) {
		return dsn
	}
	return dsn + " sslmode=disable"
}

// local says whether host is this machine: none (the default socket), a socket
// directory, localhost, or a loopback address.
func local(host string) bool {
	if host == "" || strings.HasPrefix(host, "/") || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
