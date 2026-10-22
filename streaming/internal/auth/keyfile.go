package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

// EnvKeyFile names the machine's stream signing key: the PEM RSA key the API
// on this machine signs the stream URLs with (api/internal/auth/signedurl).
// The streaming role verifies with its public half — no JWKS fetch, and a node
// verifies its own API's URLs while the home verifies the home's.
const EnvKeyFile = "MAKTABA_STREAM_SIGNING_KEY"

// ReadKeyFile reads a PEM RSA key (private PKCS#8/PKCS#1, or a PKIX public
// key) and returns its public half with the kid the API derives for it
// (sha256 of the PKIX DER, first 16 hex chars — api/internal/auth/keys).
func ReadKeyFile(path string) (string, *rsa.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return "", nil, errors.New("stream key: no PEM block")
	}
	var pub *rsa.PublicKey
	switch block.Type {
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return "", nil, err
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return "", nil, errors.New("stream key: not RSA")
		}
		pub = &rk.PublicKey
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", nil, err
		}
		pub = &k.PublicKey
	case "PUBLIC KEY":
		k, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return "", nil, err
		}
		rk, ok := k.(*rsa.PublicKey)
		if !ok {
			return "", nil, errors.New("stream key: not RSA")
		}
		pub = rk
	default:
		return "", nil, fmt.Errorf("stream key: unsupported PEM block %q", block.Type)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])[:16], pub, nil
}

// NewKeyFileCache is a key cache holding the machine's stream key. The API
// creates the file as it starts, which may come after this process starts: a
// signed URL naming a key not read yet reads the file again at once (at most
// every reloadEvery), so the first playback after a start is not refused, and
// until the file reads it is also retried every interval (until ctx ends).
func NewKeyFileCache(ctx context.Context, path string, interval time.Duration, onLoad func(kid string)) *JWKSCache {
	c := &JWKSCache{keys: map[string]*rsa.PublicKey{}, refresh: interval}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	var loaded atomic.Value // the kid read last (said once each)
	load := func() bool {
		kid, pub, err := ReadKeyFile(path)
		if err != nil {
			c.failures.Add(1)
			return false
		}
		c.Set(map[string]*rsa.PublicKey{kid: pub})
		if prev, _ := loaded.Swap(kid).(string); prev != kid && onLoad != nil {
			onLoad(kid)
		}
		return true
	}
	// a token naming a key not read yet reads the file again at once (Lookup)
	c.reload = load
	if load() {
		return c
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if load() {
					return
				}
			}
		}
	}()
	return c
}
