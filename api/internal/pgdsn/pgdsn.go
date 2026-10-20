// Package pgdsn adapts a libpq connection string to lib/pq, which has no
// sslmode=prefer or sslmode=allow: it fails with `pq: unsupported sslmode
// "prefer"`. The home hands every joined node sslmode=prefer (cluster/payload.go),
// so a full node's api could not open its database at all (Epic 35 story 35.8).
package pgdsn

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Probe connects to dsn and reports the error, if any.
type Probe func(ctx context.Context, dsn string) error

// Ping is the real Probe: one lib/pq connection, then closed.
func Ping(ctx context.Context, dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

// ForLibPQ returns dsn with a libpq-only sslmode replaced by one lib/pq takes, as
// libpq itself would settle it:
//
//   - prefer: require when the server takes SSL, disable when probe answers that it
//     does not (pq.ErrSSLNotSupported). Any other probe error keeps require: the
//     connection is never downgraded without the server saying so.
//   - allow: disable (libpq tries a plain connection first).
//
// Any other dsn comes back unchanged. Both URL and key=value forms are handled.
func ForLibPQ(ctx context.Context, dsn string, probe Probe) string {
	mode := sslMode(dsn)
	switch mode {
	case "allow":
		return withSSLMode(dsn, "disable")
	case "prefer":
		req := withSSLMode(dsn, "require")
		if err := probe(ctx, req); errors.Is(err, pq.ErrSSLNotSupported) {
			return withSSLMode(dsn, "disable")
		}
		return req
	}
	return dsn
}

var kvSSLMode = regexp.MustCompile(`(^|\s)sslmode\s*=\s*'?([A-Za-z-]*)'?`)

func isURL(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

func sslMode(dsn string) string {
	if isURL(dsn) {
		u, err := url.Parse(dsn)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Query().Get("sslmode"))
	}
	if m := kvSSLMode.FindStringSubmatch(dsn); m != nil {
		return strings.ToLower(m[2])
	}
	return ""
}

func withSSLMode(dsn, mode string) string {
	if isURL(dsn) {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		q := u.Query()
		q.Set("sslmode", mode)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return kvSSLMode.ReplaceAllString(dsn, "${1}sslmode="+mode)
}
