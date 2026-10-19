// Package agent is the home's side of the cluster channel (Epic 31 phase
// 2, specs/epics/31-multi-instance/design-agent.md): the authenticated,
// encrypted WebSocket every Maktaba process holds to the home
// (/api/cluster/agent), the hub that keeps the live state of every
// connected process, the browser WebSocket (/api/cluster/ws), the history
// endpoints that read the cluster files on the share, the per-process
// commands and the LLM router with its OpenAI-compatible door.
//
// Nothing here goes through the database: the hub reads cluster_nodes to
// authenticate a node, the library roots to find the share, and the queue
// snapshot it shows; everything the processes send stays in memory or in
// files.
package agent

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// Protocol constants of design-agent.md §2.
const (
	ProtocolVersion = 1
	NonceSize       = 16
	KeySize         = 32

	hkdfInfo  = "maktaba-agent-v1"
	authLabel = "maktaba-agent-auth"
)

// ErrFrame is returned for a sealed frame that does not open: a failed
// tag, a repeated or a skipped counter. The connection must close.
var ErrFrame = errors.New("agent: frame rejected")

// DeriveKeys returns (k_c2s, k_s2c):
// HKDF-SHA256(ikm=shared, salt=client_nonce‖server_nonce,
// info="maktaba-agent-v1", L=64) split in two.
func DeriveKeys(shared, clientNonce, serverNonce []byte) (c2s, s2c []byte, err error) {
	if len(shared) != KeySize {
		return nil, nil, errors.New("agent: shared secret must be 32 bytes")
	}
	if len(clientNonce) != NonceSize || len(serverNonce) != NonceSize {
		return nil, nil, errors.New("agent: nonces must be 16 bytes")
	}
	salt := make([]byte, 0, 2*NonceSize)
	salt = append(salt, clientNonce...)
	salt = append(salt, serverNonce...)
	okm := make([]byte, 2*KeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, salt, []byte(hkdfInfo)), okm); err != nil {
		return nil, nil, fmt.Errorf("agent: hkdf: %w", err)
	}
	return okm[:KeySize], okm[KeySize:], nil
}

// Proof is HMAC-SHA256(k_c2s, "maktaba-agent-auth"‖server_nonce).
func Proof(c2s, serverNonce []byte) []byte {
	m := hmac.New(sha256.New, c2s)
	m.Write([]byte(authLabel))
	m.Write(serverNonce)
	return m.Sum(nil)
}

// CheckProof compares a client's proof in constant time.
func CheckProof(c2s, serverNonce, proof []byte) bool {
	return subtle.ConstantTimeCompare(Proof(c2s, serverNonce), proof) == 1
}

// Cipher seals or opens one direction's frames:
// ChaCha20-Poly1305(key, nonce = 4 zero bytes ‖ 8-byte big-endian counter,
// aad = ""), the counter starting at 0 and moving by one per frame. Because
// the nonce is the expected counter, a repeated, skipped or forged frame
// fails to open. Not safe for concurrent use: each direction has one
// owner (the session's reader or writer goroutine).
type Cipher struct {
	sealer  cipherAEAD
	counter uint64
	broken  bool
}

type cipherAEAD interface {
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

// NewCipher returns a direction's cipher at counter 0.
func NewCipher(key []byte) (*Cipher, error) {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("agent: aead: %w", err)
	}
	return &Cipher{sealer: a}, nil
}

func nonceFor(counter uint64) []byte {
	n := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}

// Counter is the counter of the next frame.
func (c *Cipher) Counter() uint64 { return c.counter }

// Seal encrypts the next frame.
func (c *Cipher) Seal(plaintext []byte) ([]byte, error) {
	if c.broken || c.counter == math.MaxUint64 {
		return nil, ErrFrame
	}
	out := c.sealer.Seal(nil, nonceFor(c.counter), plaintext, nil)
	c.counter++
	return out, nil
}

// Open decrypts the next frame; any failure breaks the cipher for good.
func (c *Cipher) Open(sealed []byte) ([]byte, error) {
	if c.broken || c.counter == math.MaxUint64 {
		return nil, ErrFrame
	}
	pt, err := c.sealer.Open(nil, nonceFor(c.counter), sealed, nil)
	if err != nil {
		c.broken = true
		return nil, ErrFrame
	}
	c.counter++
	return pt, nil
}
