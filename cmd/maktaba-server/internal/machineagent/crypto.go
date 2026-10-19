// Package machineagent is a full node's machine agent (Epic 35 story 35.8, design
// §9.2): the node supervisor's Go client of the cluster channel (Epic 31 design-agent
// §4), protocol v2, kind "node", process "supervisor", worker id "sup-<node8>". It holds
// one channel per machine for Go — it survives the API's restarts — and carries what the
// home pushes to the machine: the delegation key (`keys`, story 35.9) and every
// revocation (`auth.revoked`), which it hands to the node's API over loopback (the
// per-boot token of <data_dir>/supervisor/local.token). It reports `update.state` (its
// release, platform and layout). The update commands of story 35.12 arrive here too.
package machineagent

// The handshake and sealing of design-agent §4.2–§4.4, the client's side (the home's
// side is api/internal/cluster/agent/crypto.go; both check
// shared/testdata/cluster/agent_vectors.json).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

const (
	protocolVersion = 2
	nonceSize       = 16
	keySize         = 32

	hkdfInfo    = "maktaba-agent-v2"
	authLabel   = "maktaba-agent-auth"
	serverLabel = "maktaba-agent-server"
)

var errFrame = errors.New("machineagent: frame rejected")

func transcript(hello, challenge []byte) []byte {
	h := sha256.New()
	h.Write(hello)
	h.Write(challenge)
	return h.Sum(nil)
}

func dh(priv, pub []byte) ([]byte, error) {
	if len(priv) != keySize || len(pub) != keySize {
		return nil, errors.New("machineagent: keys must be 32 bytes")
	}
	out, err := curve25519.X25519(priv, pub)
	if err != nil {
		return nil, err
	}
	var acc byte
	for _, b := range out {
		acc |= b
	}
	if acc == 0 {
		return nil, errors.New("machineagent: all-zero shared secret")
	}
	return out, nil
}

func deriveKeys(ikm, clientNonce, serverNonce []byte) (c2s, s2c []byte, err error) {
	if len(clientNonce) != nonceSize || len(serverNonce) != nonceSize {
		return nil, nil, errors.New("machineagent: nonces must be 16 bytes")
	}
	salt := append(append(make([]byte, 0, 2*nonceSize), clientNonce...), serverNonce...)
	okm := make([]byte, 2*keySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, []byte(hkdfInfo)), okm); err != nil {
		return nil, nil, fmt.Errorf("machineagent: hkdf: %w", err)
	}
	return okm[:keySize], okm[keySize:], nil
}

func mac(key []byte, label string, th []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(label))
	m.Write(th)
	return m.Sum(nil)
}

func clientProof(c2s, th []byte) []byte { return mac(c2s, authLabel, th) }
func serverProof(s2c, th []byte) []byte { return mac(s2c, serverLabel, th) }

// cipher seals or opens one direction's frames (counter nonce, aad empty).
type cipher struct {
	aead interface {
		Seal(dst, nonce, plaintext, additionalData []byte) []byte
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
	}
	counter uint64
	broken  bool
}

func newCipher(key []byte) (*cipher, error) {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return &cipher{aead: a}, nil
}

func nonceFor(counter uint64) []byte {
	n := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}

func (c *cipher) seal(pt []byte) ([]byte, error) {
	if c.broken || c.counter == math.MaxUint64 {
		return nil, errFrame
	}
	out := c.aead.Seal(nil, nonceFor(c.counter), pt, nil)
	c.counter++
	return out, nil
}

func (c *cipher) open(sealed []byte) ([]byte, error) {
	if c.broken || c.counter == math.MaxUint64 {
		return nil, errFrame
	}
	pt, err := c.aead.Open(nil, nonceFor(c.counter), sealed, nil)
	if err != nil {
		c.broken = true
		return nil, errFrame
	}
	c.counter++
	return pt, nil
}
