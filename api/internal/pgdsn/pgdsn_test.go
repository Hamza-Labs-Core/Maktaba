package pgdsn

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestForLibPQ(t *testing.T) {
	noSSL := func(context.Context, string) error { return pq.ErrSSLNotSupported }
	sslOK := func(context.Context, string) error { return nil }
	down := func(context.Context, string) error { return errors.New("dial tcp: connection refused") }
	never := func(context.Context, string) error { t.Fatal("probed"); return nil }

	const node = "postgres://node_1e943eda:pw@192.168.50.177:5432/maktaba?sslmode=prefer"
	for _, c := range []struct {
		name, dsn string
		probe     Probe
		want      string
	}{
		{"prefer, server without SSL", node, noSSL, "postgres://node_1e943eda:pw@192.168.50.177:5432/maktaba?sslmode=disable"},
		{"prefer, server with SSL", node, sslOK, "postgres://node_1e943eda:pw@192.168.50.177:5432/maktaba?sslmode=require"},
		{"prefer, server unreachable keeps SSL", node, down, "postgres://node_1e943eda:pw@192.168.50.177:5432/maktaba?sslmode=require"},
		{"allow", "postgresql://u@h/db?sslmode=allow", never, "postgresql://u@h/db?sslmode=disable"},
		{"key=value prefer", "host=h user=u sslmode=prefer dbname=db", noSSL, "host=h user=u sslmode=disable dbname=db"},
		{"key=value quoted prefer", "host=h sslmode = 'prefer' dbname=db", noSSL, "host=h sslmode = disable dbname=db"},
		// a value is read the way lib/pq reads it: no sslmode inside a quoted password
		{"key=value sslmode inside a quoted value", "host=h password='a sslmode=prefer b' dbname=db", never,
			"host=h password='a sslmode=prefer b' dbname=db"},
		{"key=value quoted value, then prefer", `host=h password='it\'s sslmode=prefer' sslmode=prefer`, noSSL,
			`host=h password='it\'s sslmode=prefer' sslmode=disable`},
		{"supported mode unchanged", "postgres://u@h/db?sslmode=disable", never, "postgres://u@h/db?sslmode=disable"},
		{"no sslmode unchanged", "postgres://u@h/db", never, "postgres://u@h/db"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ForLibPQ(context.Background(), c.dsn, c.probe); got != c.want {
				t.Fatalf("ForLibPQ(%q) = %q, want %q", c.dsn, got, c.want)
			}
		})
	}
}

func TestWithConnectTimeout(t *testing.T) {
	for _, c := range []struct {
		dsn  string
		d    time.Duration
		want string
	}{
		{"postgres://u@h/db?sslmode=prefer", 10 * time.Second, "postgres://u@h/db?connect_timeout=10&sslmode=prefer"},
		{"postgres://u@h/db?connect_timeout=3", 10 * time.Second, "postgres://u@h/db?connect_timeout=3"},
		{"postgres://u@h/db", 1500 * time.Millisecond, "postgres://u@h/db?connect_timeout=2"},
		{"host=h dbname=db", 10 * time.Second, "host=h dbname=db connect_timeout=10"},
		{"host=h connect_timeout=0", 10 * time.Second, "host=h connect_timeout=0"},
		{"host=h password='connect_timeout=1'", 10 * time.Second, "host=h password='connect_timeout=1' connect_timeout=10"},
	} {
		if got := WithConnectTimeout(c.dsn, c.d); got != c.want {
			t.Errorf("WithConnectTimeout(%q, %v) = %q, want %q", c.dsn, c.d, got, c.want)
		}
	}
}

// listen serves each connection on 127.0.0.1 with serve.
func listen(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); serve(c) }()
		}
	}()
	return ln.Addr().String()
}

func shortConnectTimeout(t *testing.T) {
	prev := ConnectTimeout
	ConnectTimeout = time.Second
	t.Cleanup(func() { ConnectTimeout = prev })
}

// TestPingStalledServer: a server that accepts and never answers held the probe, and
// the api's start before any listener bound, for as long as the socket lived (18-30 s
// seen); connect_timeout bounds the SSL negotiation and startup too.
func TestPingStalledServer(t *testing.T) {
	shortConnectTimeout(t)
	addr := listen(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
	start := time.Now()
	got := ForLibPQ(context.Background(), "postgres://node_1e943eda:pw@"+addr+"/maktaba?sslmode=prefer", Ping)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("probe took %v, want about ConnectTimeout (%v)", took, ConnectTimeout)
	}
	if want := "postgres://node_1e943eda:pw@" + addr + "/maktaba?sslmode=require"; got != want {
		t.Fatalf("ForLibPQ = %q, want %q (a stalled server keeps SSL)", got, want)
	}
}

// noSSLServer answers an SSLRequest with 'N' and a plain startup with an error (no
// login), recording which each connection opened with.
func noSSLServer(t *testing.T) (addr string, seen func() []string) {
	var mu sync.Mutex
	var opened []string
	addr = listen(t, func(c net.Conn) {
		var head [8]byte
		if _, err := io.ReadFull(c, head[:]); err != nil {
			return
		}
		n, code := binary.BigEndian.Uint32(head[:4]), binary.BigEndian.Uint32(head[4:])
		mu.Lock()
		defer mu.Unlock()
		if code == 80877103 { // SSLRequest
			opened = append(opened, "ssl")
			_, _ = c.Write([]byte{'N'})
			return
		}
		opened = append(opened, "plain")
		if _, err := io.CopyN(io.Discard, c, int64(n)-8); err != nil {
			return
		}
		var body bytes.Buffer
		for _, f := range []string{"SFATAL", "C28000", "Mfake: no login"} {
			body.WriteString(f)
			body.WriteByte(0)
		}
		body.WriteByte(0)
		msg := []byte{'E', 0, 0, 0, 0}
		binary.BigEndian.PutUint32(msg[1:], uint32(4+body.Len()))
		_, _ = c.Write(append(msg, body.Bytes()...))
	})
	return addr, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), opened...) }
}

func TestPingNoSSLServer(t *testing.T) {
	shortConnectTimeout(t)
	addr, _ := noSSLServer(t)
	dsn := "postgres://u:pw@" + addr + "/maktaba?sslmode=prefer"
	if got, want := ForLibPQ(context.Background(), dsn, Ping), "postgres://u:pw@"+addr+"/maktaba?sslmode=disable"; got != want {
		t.Fatalf("ForLibPQ = %q, want %q", got, want)
	}
}

// TestOpenPreferPerConnection: a pool settles sslmode=prefer for each new connection,
// as libpq does, instead of pinning the first answer for the process's life: every
// connection asks for SSL first, and goes plain only after the server's 'N'.
func TestOpenPreferPerConnection(t *testing.T) {
	shortConnectTimeout(t)
	addr, seen := noSSLServer(t)
	db, err := Open(WithConnectTimeout("postgres://u:pw@"+addr+"/maktaba?sslmode=prefer", ConnectTimeout))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for range 2 {
		var pqErr *pq.Error
		if err := db.Ping(); !errors.As(err, &pqErr) || pqErr.Code != "28000" {
			t.Fatalf("Ping = %v, want the server's plain-startup refusal", err)
		}
	}
	if got := strings.Join(seen(), ","); got != "ssl,plain,ssl,plain" {
		t.Fatalf("connections opened with %s, want ssl,plain,ssl,plain", got)
	}
}

type fakeConnector struct {
	err   error
	calls int
}

func (f *fakeConnector) Connect(context.Context) (driver.Conn, error) {
	f.calls++
	return nil, f.err
}
func (f *fakeConnector) Driver() driver.Driver { return pq.Driver{} }

func TestPreferConnector(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, c := range []struct {
		name       string
		secureErr  error
		plainCalls int
		wantErr    bool
	}{
		{"server takes SSL", nil, 0, false},
		{"server without SSL", pq.ErrSSLNotSupported, 1, false},
		{"other error never downgrades", errors.New("dial tcp: connection refused"), 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			secure, plain := &fakeConnector{err: c.secureErr}, &fakeConnector{}
			_, err := (&preferConnector{secure: secure, plain: plain, key: "k-" + c.name}).Connect(context.Background())
			if (err != nil) != c.wantErr || plain.calls != c.plainCalls {
				t.Fatalf("err %v, plain calls %d; want error %v, %d plain calls", err, plain.calls, c.wantErr, c.plainCalls)
			}
		})
	}

	// the settled sslmode is logged once, and again only when the server's answer changes
	logs.Reset()
	secure := &fakeConnector{err: pq.ErrSSLNotSupported}
	pc := &preferConnector{secure: secure, plain: &fakeConnector{}, key: "k-log"}
	for range 3 {
		_, _ = pc.Connect(context.Background())
	}
	secure.err = nil
	_, _ = pc.Connect(context.Background())
	if got := strings.Count(logs.String(), "db_sslmode_settled"); got != 2 ||
		!strings.Contains(logs.String(), "sslmode=disable") || !strings.Contains(logs.String(), "sslmode=require") {
		t.Fatalf("want two settled lines (disable, then require), got:\n%s", logs.String())
	}
}
