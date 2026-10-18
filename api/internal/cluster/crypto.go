// Package cluster is the home side of multi-instance Maktaba (Epic 31,
// specs/epics/31-multi-instance/design.md): the join cryptography (§3.2),
// the instance identity and machine registry store (§4) and the database
// role provisioning for joined machines (§3.3).
package cluster

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// KeySize is the size of an X25519 key (private or public), in bytes.
const KeySize = 32

const (
	infoSAS  = "maktaba-join-sas"
	infoSeal = "maktaba-join-seal"
)

// Envelope is a sealed payload on the wire: standard base64 of the 12-byte
// nonce and of the ciphertext (which carries the 16-byte Poly1305 tag).
type Envelope struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// GenerateKey returns a fresh X25519 keypair (private, public).
func GenerateKey() (priv, pub []byte, err error) {
	priv = make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, priv); err != nil {
		return nil, nil, fmt.Errorf("cluster: random key: %w", err)
	}
	pub, err = PublicKey(priv)
	if err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

// PublicKey derives the X25519 public key of priv.
func PublicKey(priv []byte) ([]byte, error) {
	if len(priv) != KeySize {
		return nil, errors.New("cluster: private key must be 32 bytes")
	}
	return curve25519.X25519(priv, curve25519.Basepoint)
}

// SharedSecret is X25519(own private, peer public). It fails on a low-order
// peer key (an all-zero result).
func SharedSecret(ownPriv, peerPub []byte) ([]byte, error) {
	if len(ownPriv) != KeySize || len(peerPub) != KeySize {
		return nil, errors.New("cluster: keys must be 32 bytes")
	}
	return curve25519.X25519(ownPriv, peerPub)
}

func derive(shared, nodePub, homePub []byte, info string, n int) ([]byte, error) {
	salt := make([]byte, 0, len(nodePub)+len(homePub))
	salt = append(salt, nodePub...)
	salt = append(salt, homePub...)
	out := make([]byte, n)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, salt, []byte(info)), out); err != nil {
		return nil, fmt.Errorf("cluster: hkdf: %w", err)
	}
	return out, nil
}

// SAS is the 6-digit pairing code both sides show / expect:
// HKDF-SHA256(shared, salt=node_pub||home_pub, info="maktaba-join-sas", L=4)
// as a big-endian integer modulo 1 000 000, zero-padded.
func SAS(shared, nodePub, homePub []byte) (string, error) {
	okm, err := derive(shared, nodePub, homePub, infoSAS, 4)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(okm)%1_000_000), nil
}

// SealKey is the ChaCha20-Poly1305 key:
// HKDF-SHA256(shared, salt=node_pub||home_pub, info="maktaba-join-seal", L=32).
func SealKey(shared, nodePub, homePub []byte) ([]byte, error) {
	return derive(shared, nodePub, homePub, infoSeal, chacha20poly1305.KeySize)
}

// Seal encrypts plaintext with key, AAD = nodeID (its canonical lowercase
// uuid), under a random nonce.
func Seal(key []byte, nodeID string, plaintext []byte) (Envelope, error) {
	nonce := make([]byte, chacha20poly1305.NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, fmt.Errorf("cluster: random nonce: %w", err)
	}
	return SealWithNonce(key, nodeID, nonce, plaintext)
}

// SealWithNonce is Seal with a caller-chosen nonce (test vectors only).
func SealWithNonce(key []byte, nodeID string, nonce, plaintext []byte) (Envelope, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return Envelope{}, fmt.Errorf("cluster: aead: %w", err)
	}
	if len(nonce) != aead.NonceSize() {
		return Envelope{}, errors.New("cluster: nonce must be 12 bytes")
	}
	ct := aead.Seal(nil, nonce, plaintext, []byte(nodeID))
	return Envelope{
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}, nil
}

// Open decrypts an envelope sealed by Seal.
func Open(key []byte, nodeID string, env Envelope) ([]byte, error) {
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return nil, fmt.Errorf("cluster: nonce: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("cluster: ciphertext: %w", err)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("cluster: aead: %w", err)
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("cluster: nonce must be 12 bytes")
	}
	return aead.Open(nil, nonce, ct, []byte(nodeID))
}

// DecodeKey parses a standard-base64 32-byte key.
func DecodeKey(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("cluster: key is not base64: %w", err)
	}
	if len(b) != KeySize {
		return nil, fmt.Errorf("cluster: key is %d bytes, want 32", len(b))
	}
	return b, nil
}

// EncodeKey is standard base64.
func EncodeKey(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
