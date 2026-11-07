package relay

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"
)

// Resume tickets (story 50.03): a home reconnects with one instead of its
// bearer secret, so a reconnect costs an HMAC, not an argon2id check.
//
//	ticket = base64url( len(server_id) ‖ server_id ‖ issued_at ‖ expires_at ‖ HMAC-SHA256(k, …) )
//	k      = HKDF-SHA256(token secret, info "tunnel-resume-v1")
//
// Every node derives the same key from the shared token secret, so a ticket
// node A issued works on node B after a failover. Rotating the token secret
// invalidates every ticket (homes fall back to their bearer). A ticket is
// checked against the relay's clock only; the home never reads its times.
// It is never logged.

// TicketTTL is how long a ticket is valid; TicketRefresh how often the relay
// sends a fresh one over the tunnel.
const (
	TicketTTL     = 10 * time.Minute
	TicketRefresh = 5 * time.Minute
)

// Ticket verification errors (story 50.03 T02).
var (
	ErrTicketMalformed   = errors.New("relay: ticket malformed")
	ErrTicketExpired     = errors.New("relay: ticket expired")
	ErrTicketBadMAC      = errors.New("relay: ticket bad_mac")
	ErrTicketWrongServer = errors.New("relay: ticket wrong_server")
)

// Tickets mints and verifies resume tickets with one derived key.
type Tickets struct {
	key []byte
	now func() time.Time
}

// NewTickets derives the ticket key from the cloud's token secret. A nil
// result (empty secret) means tickets are off: Mint returns "" and Verify
// refuses.
func NewTickets(tokenSecret []byte) *Tickets {
	if len(tokenSecret) == 0 {
		return nil
	}
	k, err := hkdf.Key(sha256.New, tokenSecret, nil, "tunnel-resume-v1", 32)
	if err != nil {
		return nil
	}
	return &Tickets{key: k, now: time.Now}
}

const macLen = sha256.Size

// Mint issues a ticket for serverID, valid TicketTTL from now.
func (t *Tickets) Mint(serverID string) string {
	if t == nil || serverID == "" || len(serverID) > 255 {
		return ""
	}
	now := t.now().Unix()
	body := make([]byte, 0, 1+len(serverID)+16+macLen)
	body = append(body, byte(len(serverID)))
	body = append(body, serverID...)
	body = binary.BigEndian.AppendUint64(body, uint64(now))
	body = binary.BigEndian.AppendUint64(body, uint64(now+int64(TicketTTL/time.Second)))
	body = append(body, t.mac(body)...)
	return base64.RawURLEncoding.EncodeToString(body)
}

func (t *Tickets) mac(b []byte) []byte {
	h := hmac.New(sha256.New, t.key)
	h.Write(b)
	return h.Sum(nil)
}

// Verify checks ticket's MAC and expiry and that it belongs to serverID. It
// does no database work: the caller then checks the server is not revoked.
func (t *Tickets) Verify(ticket, serverID string) error {
	if t == nil {
		return ErrTicketMalformed
	}
	b, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil || len(b) < 1 {
		return ErrTicketMalformed
	}
	n := int(b[0])
	if len(b) != 1+n+16+macLen {
		return ErrTicketMalformed
	}
	body, sum := b[:len(b)-macLen], b[len(b)-macLen:]
	if !hmac.Equal(sum, t.mac(body)) {
		return ErrTicketBadMAC
	}
	if string(body[1:1+n]) != serverID {
		return ErrTicketWrongServer
	}
	expires := int64(binary.BigEndian.Uint64(body[1+n+8:]))
	if t.now().Unix() >= expires {
		return ErrTicketExpired
	}
	return nil
}
