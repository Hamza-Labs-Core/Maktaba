// Package pgdsn adapts a libpq connection string to lib/pq, which has no
// sslmode=prefer or sslmode=allow: it fails with `pq: unsupported sslmode
// "prefer"`. The home hands every joined node sslmode=prefer (cluster/payload.go),
// so a full node's api could not open its database at all (Epic 35 story 35.8).
package pgdsn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

// ConnectTimeout bounds one connection's start: its TCP dial, SSL negotiation and
// startup. lib/pq bounds those only by the dsn's connect_timeout (a context covers the
// dial at most), so a server that accepts and never answers held Ping, and the node's
// role check, for as long as the socket lived. Tests shorten it.
var ConnectTimeout = 10 * time.Second

// Probe connects to dsn and reports the error, if any.
type Probe func(ctx context.Context, dsn string) error

// Ping is the real Probe: one lib/pq connection, then closed, within ConnectTimeout.
func Ping(ctx context.Context, dsn string) error {
	db, err := Open(WithConnectTimeout(dsn, ConnectTimeout))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, ConnectTimeout)
	defer cancel()
	return db.PingContext(ctx)
}

// ForLibPQ returns dsn with a libpq-only sslmode replaced by one lib/pq takes, as
// libpq itself would settle it, for a reader that takes a connection string (the
// event bus's pq.Listener, the one-shot commands); a pool opens with Open, which
// settles it for each connection instead:
//
//   - prefer: require when the server takes SSL, disable when probe answers that it
//     does not (pq.ErrSSLNotSupported). Any other probe error keeps require: the
//     connection is never downgraded without the server saying so.
//   - allow: disable (libpq tries a plain connection first).
//
// Any other dsn comes back unchanged. Both URL and key=value forms are handled.
func ForLibPQ(ctx context.Context, dsn string, probe Probe) string {
	mode := SSLMode(dsn)
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

// Open opens a lib/pq pool on dsn. sslmode=prefer is settled for each new connection
// the way libpq does it, never pinned for the pool's life: SSL, unless that server
// answers that it has none (pq.ErrSSLNotSupported), then plain; sslmode=allow is plain.
// Any other dsn opens as sql.Open("postgres", dsn) would, the context bounding the dial.
func Open(dsn string) (*sql.DB, error) {
	switch SSLMode(dsn) {
	case "allow":
		dsn = withSSLMode(dsn, "disable")
	case "prefer":
		secure, err := pq.NewConnector(withSSLMode(dsn, "require"))
		if err != nil {
			return nil, err
		}
		plain, err := pq.NewConnector(withSSLMode(dsn, "disable"))
		if err != nil {
			return nil, err
		}
		return sql.OpenDB(&preferConnector{secure: secure, plain: plain, key: dsn}), nil
	}
	c, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(c), nil
}

// preferConnector is sslmode=prefer for lib/pq: SSL first, plain when the server
// answers that it has no SSL.
type preferConnector struct {
	secure, plain driver.Connector
	key           string // the dsn, never logged: which database the log line is about
}

func (c *preferConnector) Connect(ctx context.Context) (driver.Conn, error) {
	cn, err := c.secure.Connect(ctx)
	mode := "require"
	if errors.Is(err, pq.ErrSSLNotSupported) {
		mode = "disable"
		cn, err = c.plain.Connect(ctx)
	}
	if err == nil {
		noteSSLMode(c.key, mode)
	}
	return cn, err
}

func (c *preferConnector) Driver() driver.Driver { return c.secure.Driver() }

// settled is the sslmode a prefer database last connected with, by dsn: the outcome
// is logged once, and again only when the server changes its answer.
var settled sync.Map

func noteSSLMode(key, mode string) {
	if prev, ok := settled.Swap(key, mode); ok && prev == mode {
		return
	}
	slog.Default().Info("database: sslmode=prefer settled", "sslmode", mode,
		"event", "db_sslmode_settled")
}

// WithConnectTimeout returns dsn with connect_timeout set to d (whole seconds, at
// least one) unless it already sets one.
func WithConnectTimeout(dsn string, d time.Duration) string {
	secs := strconv.Itoa(max(1, int(math.Ceil(d.Seconds()))))
	if isURL(dsn) {
		u, err := url.Parse(dsn)
		if err != nil {
			return dsn
		}
		q := u.Query()
		if q.Has("connect_timeout") {
			return dsn
		}
		q.Set("connect_timeout", secs)
		u.RawQuery = q.Encode()
		return u.String()
	}
	if len(kvFind(dsn, "connect_timeout")) > 0 {
		return dsn
	}
	return kvSet(dsn, "connect_timeout", secs)
}

func isURL(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// SSLMode is dsn's sslmode, lower case ("" when it sets none).
func SSLMode(dsn string) string {
	if isURL(dsn) {
		u, err := url.Parse(dsn)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Query().Get("sslmode"))
	}
	if vs := kvFind(dsn, "sslmode"); len(vs) > 0 {
		// the last one wins, as in lib/pq
		return strings.ToLower(vs[len(vs)-1].val)
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
	return kvSet(dsn, "sslmode", mode)
}

// kvValue is one key's value in a key=value dsn: unquoted, and where it sits in the
// dsn (quotes included).
type kvValue struct {
	val        string
	start, end int
}

// kvFind is every value of key in a key=value dsn, read the way lib/pq reads one:
// a value is single-quoted or runs to the next space, a backslash escapes the next
// character, so `password='a sslmode=prefer b'` sets no sslmode. Reading stops at
// the first malformed pair.
func kvFind(dsn, key string) []kvValue {
	var out []kvValue
	i, n := 0, len(dsn)
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' }
	for {
		for i < n && space(dsn[i]) {
			i++
		}
		if i == n {
			return out
		}
		k := i
		for i < n && dsn[i] != '=' && !space(dsn[i]) {
			i++
		}
		name := dsn[k:i]
		for i < n && space(dsn[i]) {
			i++
		}
		if i == n || dsn[i] != '=' || name == "" {
			return out
		}
		i++
		for i < n && space(dsn[i]) {
			i++
		}
		start, quoted := i, i < n && dsn[i] == '\''
		if quoted {
			i++
		}
		var val strings.Builder
		closed := !quoted
		for i < n {
			c := dsn[i]
			if c == '\\' && i+1 < n {
				val.WriteByte(dsn[i+1])
				i += 2
				continue
			}
			if quoted && c == '\'' {
				i++
				closed = true
				break
			}
			if !quoted && space(c) {
				break
			}
			val.WriteByte(c)
			i++
		}
		if !closed {
			return out
		}
		if name == key {
			out = append(out, kvValue{val: val.String(), start: start, end: i})
		}
	}
}

// kvSet sets key to val (which needs no quoting) in a key=value dsn: every value of
// key is replaced, or the pair is added in front (after a dsn ending in an empty
// value, `password=`, an appended pair would be read as that value).
func kvSet(dsn, key, val string) string {
	vs := kvFind(dsn, key)
	if len(vs) == 0 {
		if strings.TrimSpace(dsn) == "" {
			return key + "=" + val
		}
		return key + "=" + val + " " + dsn
	}
	for j := len(vs) - 1; j >= 0; j-- {
		dsn = dsn[:vs[j].start] + val + dsn[vs[j].end:]
	}
	return dsn
}
