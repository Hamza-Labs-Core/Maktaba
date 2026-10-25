package supervisor

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// jwtKeyBits is the size of the home's JWT key (the api's `keys init` makes 4096 too; the
// tests make smaller ones, faster).
var jwtKeyBits = 4096

// EnsureJWTKeys makes the home's JWT key pair (JWTKeyPaths) when it has none. The api signs
// a native app's tokens with it (the TV apps, the native Player and Remote: sign-in sends
// the tokens in the body); without one their sign-in answered 500 ("no JWT keys
// configured") while the web app, on cookies, went on. A pair already there (one a switch
// of the home handed over, or one made before) is kept as it is.
func EnsureJWTKeys() error {
	privPath, pubPath := JWTKeyPaths()
	_, errPriv := os.Stat(privPath)
	_, errPub := os.Stat(pubPath)
	switch {
	case errPriv == nil && errPub == nil:
		return nil
	case errPriv == nil || errPub == nil:
		// half a pair: never replace a private key a token may still be signed with
		return fmt.Errorf("jwt keys: %s and %s must both be there or both be gone", privPath, pubPath)
	case !errors.Is(errPriv, os.ErrNotExist) || !errors.Is(errPub, os.ErrNotExist):
		return fmt.Errorf("jwt keys: %v; %v", errPriv, errPub)
	}
	privPEM, pubPEM, err := newJWTKeyPair(jwtKeyBits)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(privPath), 0o700); err != nil {
		return err
	}
	// the public half last: a pair is whole once both are there
	if err := writeKeyFile(privPath, privPEM, 0o600); err != nil {
		return err
	}
	return writeKeyFile(pubPath, pubPEM, 0o644)
}

// newJWTKeyPair is an RSA pair as the api's keys.FromPEM reads it: PKCS#8 "PRIVATE KEY"
// and PKIX "PUBLIC KEY" blocks.
func newJWTKeyPair(bits int) (privPEM, pubPEM []byte, err error) {
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, nil, err
	}
	pub, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}), nil
}

func writeKeyFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
