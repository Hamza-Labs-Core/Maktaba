package pgbundle

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// ErrStarting: the server answers but is still starting up or recovering
// (SQLSTATE 57P03). It is alive; it does not take connections yet.
var ErrStarting = errors.New("postgres is starting up")

// ErrRefused: the server answered with another error (too many clients, a
// missing role or database). It is up; this connection was refused.
var ErrRefused = errors.New("postgres refused the connection")

// Ready reports whether the Postgres at addr takes connections, the way
// pg_isready does: it sends a startup message and reads the first answer. An
// authentication request means ready (it then hangs up, which the server
// takes silently); an error message is not ready — ErrStarting for a server
// starting up or recovering.
func Ready(ctx context.Context, addr, user, database string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, int32(196608)) // protocol 3.0
	for _, kv := range []string{"user", user, "database", database} {
		body.WriteString(kv)
		body.WriteByte(0)
	}
	body.WriteByte(0)
	msg := make([]byte, 4, 4+body.Len())
	binary.BigEndian.PutUint32(msg, uint32(4+body.Len()))
	if _, err := conn.Write(append(msg, body.Bytes()...)); err != nil {
		return err
	}

	head := make([]byte, 5)
	if _, err := io.ReadFull(conn, head); err != nil {
		return fmt.Errorf("postgres did not answer: %w", err)
	}
	switch head[0] {
	case 'R':
		return nil
	case 'E':
		n := int(binary.BigEndian.Uint32(head[1:])) - 4
		if n < 0 || n > 64<<10 {
			return errors.New("postgres sent a malformed error")
		}
		fields := make([]byte, n)
		if _, err := io.ReadFull(conn, fields); err != nil {
			return fmt.Errorf("postgres error: %w", err)
		}
		code, text := errorFields(fields)
		if code == "57P03" {
			return fmt.Errorf("%w: %s", ErrStarting, text)
		}
		return fmt.Errorf("%w: %s (%s)", ErrRefused, text, code)
	}
	return fmt.Errorf("postgres answered an unexpected %q message", head[0])
}

// errorFields picks the SQLSTATE (C) and message (M) of an ErrorResponse body.
func errorFields(b []byte) (code, msg string) {
	for len(b) > 1 && b[0] != 0 {
		t := b[0]
		end := bytes.IndexByte(b[1:], 0)
		if end < 0 {
			break
		}
		v := string(b[1 : 1+end])
		switch t {
		case 'C':
			code = v
		case 'M':
			msg = v
		}
		b = b[2+end:]
	}
	return code, msg
}
