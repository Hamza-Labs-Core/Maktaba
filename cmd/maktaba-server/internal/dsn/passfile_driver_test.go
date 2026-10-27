package dsn

import (
	"bufio"
	"database/sql"
	"encoding/binary"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// A child's driver reads the password from the passfile ProcessEnv names, on every OS:
// lib/pq before v1.11 skipped a passfile whose mode has group or world bits, which is
// every file on Windows (Go reports rw-rw-rw-), so a Windows node's api and its copy
// logged in to the home's database with no password ("password authentication failed")
// and every page that reads it answered home_unreachable.
func TestTheDriverReadsThePassfile(t *testing.T) {
	addr, sent := passwordCatcher(t)
	env, err := ProcessEnv([]string{"PGPASSWORD=inherited",
		"DATABASE_URL=postgres://maktaba:s3cret@" + addr + "/maktaba?sslmode=disable"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// the child's environment is env alone, not this process's (GitHub's Windows runners
	// set PGPASSWORD for their own Postgres)
	t.Setenv("PGPASSWORD", "")
	_ = os.Unsetenv("PGPASSWORD")
	var bare string
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if k == "DATABASE_URL" {
			bare = v
			continue
		}
		t.Setenv(k, v)
	}
	db, err := sql.Open("postgres", bare)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_ = db.Ping() // the catcher refuses every password
	select {
	case pw := <-sent:
		if pw != "s3cret" {
			t.Fatalf("the driver sent %q, not the passfile's password", pw)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the driver never answered the password request")
	}
}

// passwordCatcher is a Postgres server that asks each client for its password in clear,
// reports it on the channel and refuses it.
func passwordCatcher(t *testing.T) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	sent := make(chan string, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				r := bufio.NewReader(c)
				var n uint32
				if binary.Read(r, binary.BigEndian, &n) != nil || n < 8 {
					return
				}
				if _, err := io.CopyN(io.Discard, r, int64(n-4)); err != nil { // the startup message
					return
				}
				_, _ = c.Write([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 3}) // AuthenticationCleartextPassword
				if b, err := r.ReadByte(); err != nil || b != 'p' {
					return
				}
				if binary.Read(r, binary.BigEndian, &n) != nil || n < 5 {
					return
				}
				pw := make([]byte, n-4)
				if _, err := io.ReadFull(r, pw); err != nil {
					return
				}
				sent <- strings.TrimSuffix(string(pw), "\x00")
				msg := "SFATAL\x00C28P01\x00Mpassword authentication failed\x00\x00"
				e := []byte{'E', 0, 0, 0, 0}
				binary.BigEndian.PutUint32(e[1:], uint32(4+len(msg)))
				_, _ = c.Write(append(e, msg...))
			}(c)
		}
	}()
	return ln.Addr().String(), sent
}
