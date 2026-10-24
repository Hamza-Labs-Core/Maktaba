package main

// A machine's access to its home's database: the home owns it (its agent.EnsureAccess).
// This machine checks that it logs in to its home's database every homeDBEvery and says
// so in its update.state (home_db). One that cannot (its role refused, the database moved
// to another cluster or port: an app's move from an operator Postgres to its own, a
// restore, a new port) gets its access re-made by the home over the machine channel
// ("home.access"), which the home's cluster key authenticates, and follows it: node.json
// changed under its lock, then a new start with it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/machineagent"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// homeAccess is the access the home re-made for this machine on its current database.
type homeAccess struct {
	Role     string `json:"role"`
	Password string `json:"password"`
	// DBHost is the database's host when it is not the home's own machine; "": the host
	// this machine reaches the home's API at.
	DBHost string `json:"db_host,omitempty"`
	DBPort int    `json:"db_port"`
	DBName string `json:"db_name"`
	// CA and TLS are the database's CA (PEM) and "off" when it has no TLS, as a join's
	// credentials carry them; neither: the machine keeps how it verifies the database now
	// (never a downgrade to an unverified connection).
	CA  string `json:"database_ca,omitempty"`
	TLS string `json:"database_tls,omitempty"`
	// Term is the home's term: an access from a home of an earlier term than the one
	// node.json follows (a home that has not learned of a switch yet) is refused.
	Term int64 `json:"term,omitempty"`
}

var (
	dbHostRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,253}$`)
	dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,63}$`)
)

func (a homeAccess) check() error {
	switch {
	case a.Role == "" || strings.Trim(a.Role, "abcdefghijklmnopqrstuvwxyz0123456789_") != "":
		return fmt.Errorf("bad role %q", a.Role)
	case a.Password == "":
		return fmt.Errorf("no password")
	case a.DBPort <= 0 || a.DBPort > 65535:
		return fmt.Errorf("bad port %d", a.DBPort)
	case a.DBHost != "" && !dbHostRe.MatchString(a.DBHost):
		return fmt.Errorf("bad database host %q", a.DBHost)
	case a.DBName != "" && !dbNameRe.MatchString(a.DBName):
		return fmt.Errorf("bad database name %q", a.DBName)
	case a.CA != "":
		return config.CheckCA(a.CA)
	}
	return nil
}

// apply points n's database at the access: its host (the home's, else the host n reaches
// the home's API at), port, role, password and database, and its CA (or TLS off; neither:
// n's own kept); the url's other settings are kept.
func (a homeAccess) apply(n *config.NodeConfig) error {
	u, err := url.Parse(n.DatabaseURL)
	if err != nil || u.Scheme == "" {
		u = &url.URL{Scheme: "postgres"}
	}
	host := strings.TrimSpace(a.DBHost)
	if host == "" {
		h, err := url.Parse(n.HomeURL())
		if err != nil || h.Hostname() == "" {
			return fmt.Errorf("the home's address is not known (node.json's home url %q)", n.HomeURL())
		}
		host = h.Hostname()
	}
	u.Host = net.JoinHostPort(host, strconv.Itoa(a.DBPort))
	u.User = url.UserPassword(a.Role, a.Password)
	if a.DBName != "" {
		u.Path = "/" + a.DBName
	}
	n.DatabaseURL = u.String()
	switch {
	case a.CA != "":
		n.DatabaseCA, n.DatabaseTLS = a.CA, ""
	case strings.EqualFold(a.TLS, "off"):
		n.DatabaseCA, n.DatabaseTLS = "", "off"
	}
	return nil
}

// homeDBTarget is where n logs in to the home's database: host:port/database as role
// (never its password), as the home compares it with what it hands out.
func homeDBTarget(n config.NodeConfig) string {
	u, err := url.Parse(n.DatabaseURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host + u.Path
}

// credsRe are a login's secrets as a driver's error may quote them (a url's user and
// password, a key=value password).
var credsRe = regexp.MustCompile(`(?i)(postgres(?:ql)?://)[^@\s/]*@|(password=)\S+`)

// noCreds is an error's text without any login's secrets in it (it goes to the log, the
// home and its pages).
func noCreds(s string) string {
	return credsRe.ReplaceAllStringFunc(s, func(m string) string {
		if i := strings.Index(m, "://"); i >= 0 && strings.HasSuffix(m, "@") {
			return m[:i+3] + "…@"
		}
		return m[:len("password=")] + "…"
	})
}

// homeAccessAddr is where an access points (host:port and role: never its password), for
// the log.
func homeAccessAddr(n config.NodeConfig) string {
	u, err := url.Parse(n.DatabaseURL)
	if err != nil {
		return "?"
	}
	return u.Host + " as " + u.User.Username()
}

// homeAccessReloadAfter is how long this machine waits, once it has kept the new access,
// before it starts again (its answer to the home goes out first); the tests shorten it.
var homeAccessReloadAfter = time.Second

// homeAccess takes the access the home re-made over the machine channel: node.json keeps
// it, and this machine starts again with it. The same access changes nothing.
func (s *switching) homeAccess(args json.RawMessage) (ok bool, detail any) {
	var a homeAccess
	if err := json.Unmarshal(args, &a); err != nil {
		return false, "bad arguments"
	}
	if err := a.check(); err != nil {
		return false, err.Error()
	}
	n := supervisor.NodeConfig()
	if !n.Joined() {
		return false, "this machine has not joined a home"
	}
	// a switch of the home runs here, or this machine follows a new home already (it
	// starts again with it): the access of the home it leaves is not taken
	if why := s.switchingNow(); why != "" {
		return false, "not now: " + why
	}
	if a.Term < n.Home.Term {
		return false, fmt.Sprintf("from a home of term %d; this machine follows the home of term %d", a.Term, n.Home.Term)
	}
	before := n.DatabaseURL + "\x00" + n.DatabaseCA + "\x00" + n.DatabaseTLS
	if err := a.apply(&n); err != nil {
		return false, err.Error()
	}
	if n.DatabaseURL+"\x00"+n.DatabaseCA+"\x00"+n.DatabaseTLS == before {
		return true, map[string]any{"changed": false}
	}
	if err := updateNodeConfig(n.NodeID, func(c *config.NodeConfig) { _ = a.apply(c) }); err != nil {
		return false, err.Error()
	}
	fmt.Fprintf(os.Stderr, "[serve] node: the home's database moved, or this machine's access to it was re-made: "+
		"following it at %s, starting again\n", homeAccessAddr(n))
	if w := s.homeDBWatch(); w != nil {
		w.healing()
	}
	after := homeAccessReloadAfter // read here: a test changes it only between commands
	go func() {
		time.Sleep(after) // the answer goes out first
		requestReload()
	}()
	return true, map[string]any{"changed": true}
}

// homeDBEvery is how often this machine checks that it logs in to its home's database;
// homeDBRecheck how soon it tries again after a failure (one failure is not told to the
// home as one to act on: machineagent.ConfirmFails).
var (
	homeDBFirst   = 5 * time.Second // after the start
	homeDBEvery   = 30 * time.Second
	homeDBRecheck = 10 * time.Second
)

// homeDBWatch keeps this machine's last login to its home's database (update.state's
// home_db).
type homeDBWatch struct {
	// probe logs in to the home's database once (tests replace it)
	probe func(ctx context.Context) error
	// target is where it logs in (host:port/database: never the password; tests replace it)
	target func() string
	// onChange is called when the state changes (the machine agent sends it again)
	onChange func()
	// kick asks run for a check now (the machine agent connected again)
	kick chan struct{}
	// first, every and recheck are run's pauses (0: homeDBFirst, homeDBEvery, homeDBRecheck;
	// tests shorten them here, never in the package's)
	first, every, recheck time.Duration

	mu sync.Mutex
	st *machineagent.HomeDB
}

func newHomeDBWatch(onChange func()) *homeDBWatch {
	return &homeDBWatch{probe: probeHomeDB, target: func() string { return homeDBTarget(supervisor.NodeConfig()) },
		onChange: onChange, kick: make(chan struct{}, 1)}
}

// reconnected forgets the last check (it may predate the home's start: a failure while
// the home was away) and asks for one now: the home hears from a check made after it
// heard from this machine again.
func (w *homeDBWatch) reconnected() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.st != nil && !w.st.Healing {
		w.st = nil
	}
	w.mu.Unlock()
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// State is the last check (nil before the first).
func (w *homeDBWatch) State() *machineagent.HomeDB {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st == nil {
		return nil
	}
	c := *w.st
	return &c
}

// healing marks the state: the home re-made this machine's access, which it follows.
func (w *homeDBWatch) healing() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if w.st == nil {
		w.st = &machineagent.HomeDB{}
	}
	w.st.Healing, w.st.At = true, time.Now().UTC()
	w.mu.Unlock()
	if w.onChange != nil {
		w.onChange()
	}
}

// check logs in once and keeps the outcome.
func (w *homeDBWatch) check(ctx context.Context) {
	err := w.probe(ctx)
	next := &machineagent.HomeDB{OK: err == nil, At: time.Now().UTC()}
	if w.target != nil {
		next.Target = w.target()
	}
	if err != nil {
		next.Code, next.Detail = classifyHomeDB(err), noCreds(err.Error())
	}
	w.mu.Lock()
	prev := w.st
	if prev != nil && prev.Healing && err != nil {
		next.Healing = true // following the new access: the next start logs in with it
	}
	if err != nil {
		next.Fails = 1
		if prev != nil && !prev.OK {
			next.Fails = prev.Fails + 1
		}
	}
	confirmed := next.Fails >= machineagent.ConfirmFails && (prev == nil || prev.Fails < machineagent.ConfirmFails)
	changed := prev == nil || prev.OK != next.OK || prev.Code != next.Code || prev.Healing != next.Healing || confirmed
	w.st = next
	w.mu.Unlock()
	if !changed {
		return
	}
	switch {
	case err != nil && !next.Healing && confirmed:
		fmt.Fprintf(os.Stderr, "[serve] node: cannot log in to the home's database at %s (%s): %s; the home "+
			"re-makes this machine's access when it is its to mend\n", next.Target, next.Code, next.Detail)
	case err == nil && prev != nil && !prev.OK:
		fmt.Fprintln(os.Stderr, "[serve] node: the home's database is reachable again")
	}
	if w.onChange != nil {
		w.onChange()
	}
}

// run checks soon after the start, then every homeDBEvery (homeDBRecheck after a
// failure), until ctx ends.
func (w *homeDBWatch) run(ctx context.Context) {
	pause := func(d, def time.Duration) time.Duration {
		if d > 0 {
			return d
		}
		return def
	}
	first, every, recheck := pause(w.first, homeDBFirst), pause(w.every, homeDBEvery), pause(w.recheck, homeDBRecheck)
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.kick:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}
		w.check(ctx)
		next := every
		if st := w.State(); st != nil && !st.OK {
			next = recheck
		}
		t.Reset(next)
	}
}

// probeHomeDB logs in to the home's database with node.json's access, as this machine's
// programs do.
func probeHomeDB(ctx context.Context) error {
	n := supervisor.NodeConfig()
	if !n.Joined() {
		return nil
	}
	dsn, err := n.HomeDSN(config.KeysDir())
	if err != nil {
		return fmt.Errorf("%s", noCreds(err.Error()))
	}
	return pingDSN(ctx, dsn)
}

// pingDSN opens dsn and pings it (lib/pq: a dsn that prefers TLS tries it, then none
// when the server has no TLS).
func pingDSN(ctx context.Context, dsn string) error {
	modes := []string{""}
	u, err := url.Parse(dsn)
	if err == nil {
		switch strings.ToLower(u.Query().Get("sslmode")) {
		case "", "prefer", "allow":
			modes = []string{"require", "disable"}
		}
	}
	var last error
	for _, m := range modes {
		d := dsn
		if m != "" && u != nil {
			q := u.Query()
			q.Set("sslmode", m)
			c := *u
			c.RawQuery = q.Encode()
			d = c.String()
		}
		err := pingOnce(ctx, d)
		if err == nil {
			return nil
		}
		if last == nil || classifyHomeDB(last) != "auth_failed" {
			last = err // a refused password on the TLS try stays the reason over a plain try's
		}
		low := strings.ToLower(err.Error())
		if m == "require" && (strings.Contains(low, "ssl is not enabled") || classifyHomeDB(err) == "auth_failed" ||
			strings.Contains(low, "pg_hba")) {
			continue // the server has no TLS, or refused this try: as the dsn allows, without
		}
		return last
	}
	return last
}

func pingOnce(ctx context.Context, dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

// classifyHomeDB is why a login to the home's database failed.
func classifyHomeDB(err error) string {
	msg := strings.ToLower(err.Error())
	has := func(s ...string) bool {
		for _, x := range s {
			if strings.Contains(msg, x) {
				return true
			}
		}
		return false
	}
	switch {
	case has("password authentication failed", "28p01", "is not permitted to log in") ||
		(has("role ") && has("does not exist")):
		return "auth_failed"
	case has("pg_hba.conf", "no pg_hba"):
		return "no_hba"
	case has("database ") && has("does not exist"), has("3d000"):
		return "no_database"
	case has("connection refused", "actively refused"):
		return "refused"
	case has("x509", "certificate", "tls:", "ssl"):
		return "tls"
	case has("timeout", "deadline exceeded", "no route to host", "no such host", "network is unreachable",
		"i/o timeout", "host is down"):
		return "unreachable"
	}
	return "other"
}
