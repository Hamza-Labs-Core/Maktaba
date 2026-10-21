package nodecopy

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

func testCopy(t *testing.T) Copy {
	t.Helper()
	dir := t.TempDir()
	return Copy{NodeID: "6F1C9A2E-0000-4000-8000-000000000001",
		HomeDSN: "postgres://maktaba_node_x:pw@192.168.1.20:54329/maktaba?sslmode=require",
		Dir:     filepath.Join(dir, "copy"), Keys: filepath.Join(dir, "keys")}
}

func TestNamesFollowTheHome(t *testing.T) {
	c := testCopy(t)
	if c.Role() != "maktaba_rep_6f1c9a2e000040008000000000000001" || c.Slot() != "maktaba_copy_6f1c9a2e000040008000000000000001" {
		t.Fatalf("names: %s %s", c.Role(), c.Slot())
	}
	if c.Addr() != "127.0.0.1:54330" || c.Database() != "maktaba" {
		t.Fatalf("address: %s %s", c.Addr(), c.Database())
	}
}

// The password is chosen once and kept (the programs' environment has it before the
// home is asked), readable by this account only.
func TestThePasswordIsKept(t *testing.T) {
	c := testCopy(t)
	pw, err := c.Password()
	if err != nil || len(pw) < 32 {
		t.Fatalf("password %q %v", pw, err)
	}
	again, _ := c.Password()
	if again != pw {
		t.Fatal("a second password was made")
	}
	dsn, _ := c.DSN()
	if !strings.Contains(dsn, c.Role()+":"+pw+"@127.0.0.1:54330/maktaba") || !strings.Contains(dsn, "sslmode=disable") {
		t.Fatalf("dsn %s", dsn)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(c.Keys, passwordFile)); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("password file: %v %v", info, err)
		}
	}
}

// The replication's connection: the passfile (never the password itself), the CA once
// pinned, quoted for both libpq and postgresql.conf.
func TestTheReplicationConnection(t *testing.T) {
	c := testCopy(t)
	c.Keys = filepath.Join(t.TempDir(), "it's keys")
	conn, err := c.conninfo()
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := c.Password()
	if strings.Contains(conn, pw) || !strings.Contains(conn, "host='192.168.1.20' port='54329'") ||
		!strings.Contains(conn, "sslmode='prefer'") || !strings.Contains(conn, `it\'s keys`) {
		t.Fatalf("conninfo before a CA: %s", conn)
	}
	ca := "-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n"
	if err := c.writeAccess(Grant{CA: &ca}); err != nil {
		t.Fatal(err)
	}
	conn, _ = c.conninfo()
	if !strings.Contains(conn, "sslmode='verify-ca'") || !strings.Contains(conn, "sslrootcert=") {
		t.Fatalf("conninfo with the CA: %s", conn)
	}
	// a later grant does not replace the pinned CA
	other := "-----BEGIN CERTIFICATE-----\nBBB\n-----END CERTIFICATE-----\n"
	_ = c.writeAccess(Grant{CA: &other})
	if b, _ := os.ReadFile(filepath.Join(c.Keys, caFile)); string(b) != ca {
		t.Fatal("the pinned CA was replaced")
	}
	pass, _ := os.ReadFile(filepath.Join(c.Keys, passFile))
	if string(pass) != "192.168.1.20:54329:*:"+c.Role()+":"+pw+"\n" {
		t.Fatalf("passfile %q", pass)
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

func TestRebuildReason(t *testing.T) {
	g := Grant{SystemID: "7300", Version: 160015}
	mk := Marker{SystemID: "7300", Major: "16"}
	for _, c := range []struct {
		mk    Marker
		built bool
		g     Grant
		want  string
	}{
		{Marker{}, false, g, "no copy"},
		{mk, true, g, ""},
		{Marker{SystemID: "7301", Major: "16"}, true, g, "another one"},
		{mk, true, Grant{SystemID: "7300", Version: 170002}, "major version"},
		{mk, true, Grant{SystemID: "7300", Version: 160015, SlotMade: true}, "gave up"},
	} {
		if got := rebuildReason(c.mk, c.built, c.g); (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Fatalf("%+v %v %+v: %q, want %q", c.mk, c.built, c.g, got, c.want)
		}
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
		{&pq.Error{Code: "57P03"}, kindUnreachable},
		{&pq.Error{Code: "28P01"}, kindRefused},
		{&pq.Error{Code: "28000"}, kindRefused},
		{&pq.Error{Code: "42501"}, kindRefused},
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
	_ = c.writeAccess(Grant{})
	if err := os.MkdirAll(c.Data(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.Wipe(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{c.Data(), filepath.Join(c.Keys, passwordFile), filepath.Join(c.Keys, passFile)} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s kept", p)
		}
	}
}
