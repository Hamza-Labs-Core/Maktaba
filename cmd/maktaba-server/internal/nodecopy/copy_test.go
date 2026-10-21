package nodecopy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

func testCopy(t *testing.T) Copy {
	t.Helper()
	dir := t.TempDir()
	return Copy{NodeID: "6F1C9A2E-0000-4000-8000-000000000001",
		HomeDSN: "postgres://maktaba_node_x:pw@192.168.1.20:54329/maktaba?sslmode=require",
		DB:      pgbundle.Bundle{Data: filepath.Join(dir, "postgres"), Keys: filepath.Join(dir, "keys"), Port: 54329}}
}

func TestNamesFollowTheHome(t *testing.T) {
	c := testCopy(t)
	if c.Role() != "maktaba_rep_6f1c9a2e000040008000000000000001" || c.Slot() != "maktaba_copy_6f1c9a2e000040008000000000000001" ||
		c.Publication() != c.Slot() {
		t.Fatalf("names: %s %s %s", c.Role(), c.Slot(), c.Publication())
	}
	if c.Addr() != "127.0.0.1:54329" {
		t.Fatalf("address: %s", c.Addr())
	}
}

// The passwords are chosen once and kept (the programs' environment has the copy's
// address before the home is asked), readable by this account only; the programs read
// the copy as the read-only role.
func TestThePasswordsAreKept(t *testing.T) {
	c := testCopy(t)
	pw, err := c.Password()
	if err != nil || len(pw) < 32 {
		t.Fatalf("password %q %v", pw, err)
	}
	if again, _ := c.Password(); again != pw {
		t.Fatal("a second password was made")
	}
	rp, _ := c.ReaderPassword()
	if rp == pw || len(rp) < 32 {
		t.Fatalf("the reader's password %q", rp)
	}
	dsn, _ := c.DSN()
	if !strings.Contains(dsn, Reader+":"+rp+"@127.0.0.1:54329/maktaba") || !strings.Contains(dsn, "sslmode=disable") {
		t.Fatalf("dsn %s", dsn)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(c.DB.Keys, passwordFile)); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("password file: %v %v", info, err)
		}
	}
}

// The subscription's connection: the home's database, the passfile (never the password
// itself), the CA once pinned, quoted for libpq.
func TestTheSubscriptionsConnection(t *testing.T) {
	c := testCopy(t)
	c.DB.Keys = filepath.Join(t.TempDir(), "it's keys")
	// a home this machine cannot verify yet gets no subscription
	if _, err := c.conninfo(""); !errors.Is(err, ErrNotVerified) || c.Verified() {
		t.Fatalf("conninfo before the home's CA: %v", err)
	}
	// with the CA node.json pins (HomeDSN): verify-ca with it
	ca := filepath.Join(c.DB.Keys, "home-database-ca.crt")
	c.HomeDSN = (&url.URL{Scheme: "postgres", User: url.UserPassword("maktaba_node_x", "pw"), Host: "192.168.1.20:54329",
		Path: "/maktaba", RawQuery: url.Values{"sslmode": {"verify-ca"}, "sslrootcert": {filepath.ToSlash(ca)}}.Encode()}).String()
	conn, err := c.conninfo("")
	if err != nil || !c.Verified() {
		t.Fatal(err)
	}
	pw, _ := c.Password()
	if strings.Contains(conn, pw) || !strings.Contains(conn, "host='192.168.1.20' port='54329' dbname='maktaba'") ||
		!strings.Contains(conn, "sslmode='verify-ca'") || !strings.Contains(conn, `it\'s keys`) ||
		!strings.Contains(conn, "sslrootcert=") {
		t.Fatalf("conninfo with the CA: %s", conn)
	}
	if conn, _ := c.conninfo("other"); !strings.Contains(conn, "dbname='other'") {
		t.Fatalf("conninfo for another database: %s", conn)
	}
	// a grant's CA is never pinned: it came over the connection it would verify
	other := "-----BEGIN CERTIFICATE-----\nBBB\n-----END CERTIFICATE-----\n"
	if err := c.writeAccess(Grant{CA: &other}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.DB.Keys, caFile)); !os.IsNotExist(err) {
		t.Fatal("a grant's CA was pinned")
	}
	pass, _ := os.ReadFile(filepath.Join(c.DB.Keys, passFile))
	if string(pass) != "192.168.1.20:54329:*:"+c.Role()+":"+pw+"\n" {
		t.Fatalf("passfile %q", pass)
	}
	// plain only on the home's word that its database has no TLS (node.json)
	c.HomeDSN = "postgres://maktaba_node_x:pw@192.168.1.20:54329/maktaba?sslmode=disable"
	if conn, err := c.conninfo(""); err != nil || !strings.Contains(conn, "sslmode='disable'") {
		t.Fatalf("conninfo with TLS off: %s %v", conn, err)
	}
}

// The join's sslmode=prefer (which lib/pq refuses) is settled as libpq settles it.
func TestTheHomesSSLModeIsSettled(t *testing.T) {
	c := testCopy(t)
	if got := withSSLMode(c.HomeDSN, "disable"); !strings.Contains(got, "sslmode=disable") || strings.Contains(got, "require") {
		t.Fatalf("withSSLMode: %s", got)
	}
	for dsn, want := range map[string]string{
		"postgres://u:p@h:5432/m?sslmode=prefer":  "prefer",
		"postgresql://u:p@h:5432/m?sslmode=Allow": "allow",
		"postgres://u:p@h:5432/m":                 "",
		"host=h user=u sslmode=prefer dbname=m":   "",
	} {
		if got := sslMode(dsn); got != want {
			t.Fatalf("sslMode(%q) = %q, want %q", dsn, got, want)
		}
	}
	// a home with no TLS: the prefer dsn connects in the clear (a server that answers
	// "no SSL" to the request, then hangs up)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 8)
				if _, err := io.ReadFull(conn, buf); err == nil {
					_, _ = conn.Write([]byte("N")) // SSLRequest: no
				}
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	c.HomeDSN = "postgres://u:p@" + ln.Addr().String() + "/m?sslmode=prefer&connect_timeout=2"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db, err := c.openHome(ctx)
	if err != nil {
		t.Fatalf("a prefer dsn against a server with no TLS: %v", err)
	}
	defer db.Close()
	// the plain connection is tried (this fake never finishes a startup): lib/pq did
	// not refuse the dsn itself
	if err := db.PingContext(ctx); err != nil && strings.Contains(err.Error(), "sslmode") {
		t.Fatalf("the dsn was refused for its sslmode: %v", err)
	}
}

// A database this machine had before it joined is kept, moved aside, never emptied.
func TestClaimKeepsADatabaseOfThisMachinesOwn(t *testing.T) {
	c := testCopy(t)
	if aside, err := c.Claim(time.Now()); err != nil || aside != "" {
		t.Fatalf("nothing there: %q %v", aside, err)
	}
	if err := os.MkdirAll(c.DB.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(c.DB.Data, "PG_VERSION"), []byte("16\n"), 0o600)
	aside, err := c.Claim(time.Date(2026, 10, 21, 1, 2, 3, 0, time.UTC))
	if err != nil || !strings.HasSuffix(aside, ".before-copy-20261021-010203") {
		t.Fatalf("a database of its own: %q %v", aside, err)
	}
	if _, err := os.Stat(filepath.Join(aside, "PG_VERSION")); err != nil {
		t.Fatal("the moved database lost its files")
	}
	// the copy's own is kept where it is
	_ = os.MkdirAll(c.DB.Data, 0o700)
	_ = os.WriteFile(filepath.Join(c.DB.Data, "PG_VERSION"), []byte("16\n"), 0o600)
	if err := c.writeMark(Marker{}); err != nil {
		t.Fatal(err)
	}
	if aside, err := c.Claim(time.Now()); err != nil || aside != "" {
		t.Fatalf("the copy's own: %q %v", aside, err)
	}
}

func TestResubscribeReason(t *testing.T) {
	c := testCopy(t)
	m := &Manager{Copy: c}
	g := Grant{SystemID: "7300", SlotExists: true}
	if why := m.resubscribeReason(g, 144); !strings.Contains(why, "no copy") {
		t.Fatalf("no marker: %q", why)
	}
	_ = os.MkdirAll(c.DB.Data, 0o700)
	_ = c.writeMark(Marker{SystemID: "7300", LocalSchema: 144, Subscribed: time.Now()})
	for _, tc := range []struct {
		g     Grant
		local int64
		want  string
	}{
		{g, 144, ""},
		{Grant{SystemID: "7301", SlotExists: true}, 144, "another one"},
		{g, 145, "updated"},
		{Grant{SystemID: "7300"}, 144, "gave up"},
	} {
		if got := m.resubscribeReason(tc.g, tc.local); (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Fatalf("%+v %d: %q, want %q", tc.g, tc.local, got, tc.want)
		}
	}
	// errors applying the home's changes for long: subscribe anew
	now := time.Now()
	m.Now = func() time.Time { return now }
	m.noteErrors(Status{Errors: 3, State: StateBehind})
	now = now.Add(11 * time.Minute)
	if got := m.resubscribeReason(g, 144); !strings.Contains(got, "keep failing") {
		t.Fatalf("stuck: %q", got)
	}
}

func TestKindOf(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	for _, c := range []struct {
		err  error
		want int
	}{
		{dial, kindUnreachable},
		{context.DeadlineExceeded, kindUnreachable},
		{&pq.Error{Code: "57P03"}, kindUnreachable}, // starting up
		{&pq.Error{Code: "57P01"}, kindUnreachable},
		{&pq.Error{Code: "53300"}, kindUnreachable}, // too many clients
		{&pq.Error{Code: "08006"}, kindUnreachable},
		// refused: the home says this machine may have no copy, or its role may no longer
		// sign in (a revocation); a permission error elsewhere, a password or a pg_hba
		// rule is not that
		{&pq.Error{Code: "MK403"}, kindRefused},
		{&pq.Error{Code: "28000", Message: `role "maktaba_node_x" is not permitted to log in`}, kindRefused},
		{&pq.Error{Code: "28000", Message: `no pg_hba.conf entry for host "10.0.0.5"`}, kindOther},
		{&pq.Error{Code: "28P01"}, kindOther},
		{&pq.Error{Code: "42501"}, kindOther},
		{&pq.Error{Code: "42883"}, kindUnsupported},
		{&pq.Error{Code: "0A000"}, kindUnsupported},
		{&pq.Error{Code: "55006"}, kindBusy},
		{errors.New("something else"), kindOther},
	} {
		if got := kindOf(c.err); got != c.want {
			t.Fatalf("%v: %d, want %d", c.err, got, c.want)
		}
	}
}

func TestWipeLeavesNothing(t *testing.T) {
	c := testCopy(t)
	_, _ = c.Password()
	_, _ = c.ReaderPassword()
	_ = c.writeAccess(Grant{})
	if err := os.MkdirAll(c.DB.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{pgbundle.SuperUser, pgbundle.AppRole} {
		_ = os.WriteFile(filepath.Join(c.DB.Keys, "postgres-"+r+".password"), []byte("x"), 0o600)
	}
	// a standby home's kit of the home's secrets goes with the copy
	for _, f := range []string{StandbyKitFile, StandbyKitMeta, StandbyKitPart} {
		_ = os.WriteFile(filepath.Join(c.DB.Keys, f), []byte("x"), 0o600)
	}
	if err := c.Wipe(); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(c.DB.Keys)
	if _, err := os.Stat(c.DB.Data); err == nil || len(left) != 0 {
		t.Fatalf("kept: data %v, keys %v", err, left)
	}
}

// A copy behind only because its beat is late (the home's API, which writes it, not
// running) follows while its slot, measured lately, is caught up with the home's WAL; a
// paused one, one far behind, or one measured long ago does not.
func TestALateBeatWithTheSlotCaughtUpFollows(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	m := &Manager{Now: func() time.Time { return now }}
	late := Status{State: StateBehind, late: true}
	m.slotLag, m.slotAt = 4096, now.Add(-time.Minute)
	if s := m.withSlot(late); s.State != StateFollowing || s.Detail == "" {
		t.Fatalf("caught up: %+v", s)
	}
	if s := m.withSlot(Status{State: StateBehind}); s.State != StateBehind {
		t.Fatalf("a paused subscription: %+v", s)
	}
	for _, c := range []struct {
		name string
		lag  int64
		at   time.Time
	}{
		{"far behind", CaughtUp + 1, now.Add(-time.Minute)},
		{"no slot", -1, now.Add(-time.Minute)},
		{"measured long ago", 0, now.Add(-10 * time.Minute)},
		{"never measured", 0, time.Time{}},
	} {
		m.slotLag, m.slotAt = c.lag, c.at
		if s := m.withSlot(late); s.State != StateBehind {
			t.Errorf("%s: %+v", c.name, s)
		}
	}
}
