package pgbundle

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeServer answers one startup message with reply and records what it got.
func fakeServer(t *testing.T, reply []byte) (string, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		head := make([]byte, 4)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		body := make([]byte, binary.BigEndian.Uint32(head)-4)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		got <- string(body)
		_, _ = conn.Write(reply)
	}()
	return ln.Addr().String(), got
}

func message(kind byte, body []byte) []byte {
	out := []byte{kind, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(out[1:], uint32(4+len(body)))
	return append(out, body...)
}

func errorResponse(code, msg string) []byte {
	b := []byte("SFATAL\x00C" + code + "\x00M" + msg + "\x00\x00")
	return message('E', b)
}

func TestReadyOnAuthRequest(t *testing.T) {
	t.Parallel()
	addr, got := fakeServer(t, message('R', []byte{0, 0, 0, 10, 'S', 'C', 'R', 'A', 'M', 0, 0}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Ready(ctx, addr, "maktaba", "maktaba"); err != nil {
		t.Fatal(err)
	}
	body := <-got
	if !strings.Contains(body, "user\x00maktaba\x00database\x00maktaba\x00") || binary.BigEndian.Uint32([]byte(body[:4])) != 196608 {
		t.Fatalf("startup message = %q", body)
	}
}

func TestReadyStartingUp(t *testing.T) {
	t.Parallel()
	addr, _ := fakeServer(t, errorResponse("57P03", "the database system is starting up"))
	err := Ready(context.Background(), addr, "u", "d")
	if !errors.Is(err, ErrStarting) || !strings.Contains(err.Error(), "starting up") {
		t.Fatalf("Ready = %v, want ErrStarting", err)
	}
}

func TestReadyOtherError(t *testing.T) {
	t.Parallel()
	addr, _ := fakeServer(t, errorResponse("53300", "too many clients"))
	err := Ready(context.Background(), addr, "u", "d")
	if !errors.Is(err, ErrRefused) || errors.Is(err, ErrStarting) || !strings.Contains(err.Error(), "too many clients (53300)") {
		t.Fatalf("Ready = %v", err)
	}
}

func TestReadyNothingListening(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	if err := Ready(context.Background(), addr, "u", "d"); err == nil {
		t.Fatal("Ready with nothing listening must fail")
	}
}
