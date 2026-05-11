package serverkeys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Sealer hides the at-rest encryption helper from this package's
// own surface. Plan-10-14 provides the production implementation
// (XChaCha20-Poly1305 against a master KEK); tests inject a stub.
type Sealer interface {
	Seal(plaintext []byte) ([]byte, error)
	Open(ciphertext []byte) ([]byte, error)
}

// LoadOptions configures Load. Exactly one of EnvPEM / DiskDir
// being populated picks the source; both may be set, in which
// case the env-var path wins (AC-2).
type LoadOptions struct {
	// EnvPEM is the raw value of MAKTABA_SERVER_IDENTITY_PRIVATE_PEM
	// (or empty if unset). Wins over any on-disk material.
	EnvPEM string

	// DiskDir is the directory under which sealed material lives.
	// `${MAKTABA_STATE_DIR}/identity/` in production. Created if
	// missing.
	DiskDir string

	// AllowNewIdentity bypasses the `expected.kid` sentinel check
	// that protects against accidental regeneration after a
	// state-dir wipe (AC edge case).
	AllowNewIdentity bool

	// Sealer wraps the private bytes on the way to disk.
	Sealer Sealer

	// Clock is used for CreatedAt stamps on generated keys.
	Clock Clock
}

// Load returns the active key and a boolean indicating whether it
// was generated fresh during this call (true) or recovered from
// existing material (false).
func Load(opts LoadOptions) (*Key, bool, error) {
	clk := opts.Clock
	if clk == nil {
		clk = SystemClock
	}

	if strings.TrimSpace(opts.EnvPEM) != "" {
		key, err := ParsePrivatePEM([]byte(opts.EnvPEM), clk.Now(), "env")
		if err != nil {
			return nil, false, fmt.Errorf("serverkeys: env: %w", err)
		}
		return key, false, nil
	}

	if opts.DiskDir == "" {
		return nil, false, errors.New("serverkeys: either EnvPEM or DiskDir must be set")
	}
	if opts.Sealer == nil {
		return nil, false, errors.New("serverkeys: Sealer is required for disk-backed loading")
	}

	if err := os.MkdirAll(opts.DiskDir, 0o700); err != nil {
		return nil, false, fmt.Errorf("serverkeys: mkdir %s: %w", opts.DiskDir, err)
	}

	unlock, err := lockDir(opts.DiskDir)
	if err != nil {
		return nil, false, err
	}
	defer unlock()

	// Try to recover an existing key first.
	existing, err := readActive(opts.DiskDir, opts.Sealer, clk)
	switch {
	case err == nil:
		if err := checkExpectedKid(opts.DiskDir, existing.Kid, opts.AllowNewIdentity); err != nil {
			return nil, false, err
		}
		return existing, false, nil
	case errors.Is(err, os.ErrNotExist):
		// fall through to generate
	default:
		return nil, false, err
	}

	// No existing material — but if `expected.kid` is present, the
	// operator told us to expect continuity, so refuse unless
	// AllowNewIdentity is set.
	if expectedKidExists(opts.DiskDir) && !opts.AllowNewIdentity {
		return nil, false, fmt.Errorf("serverkeys: expected.kid sentinel present at %s but no key material on disk; refusing to generate a fresh identity (pass --allow-new-identity to override)", opts.DiskDir)
	}

	fresh, err := Generate(clk.Now(), "generated")
	if err != nil {
		return nil, false, err
	}
	if err := writeKey(opts.DiskDir, fresh, opts.Sealer); err != nil {
		return nil, false, err
	}
	if err := writeExpectedKid(opts.DiskDir, fresh.Kid); err != nil {
		return nil, false, err
	}
	return fresh, true, nil
}

const (
	sealedSuffix = ".pem.sealed"
	pubSuffix    = ".pub.pem"
	currentKid   = "current.kid"
	expectedKid  = "expected.kid"
)

func readActive(dir string, sealer Sealer, clk Clock) (*Key, error) {
	kid, err := os.ReadFile(filepath.Join(dir, currentKid))
	if err != nil {
		return nil, err
	}
	kidStr := strings.TrimSpace(string(kid))
	if kidStr == "" {
		return nil, os.ErrNotExist
	}
	sealed, err := os.ReadFile(filepath.Join(dir, kidStr+sealedSuffix))
	if err != nil {
		return nil, err
	}
	pemBytes, err := sealer.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("serverkeys: unseal: %w", err)
	}
	key, err := ParsePrivatePEM(pemBytes, clk.Now(), "disk")
	if err != nil {
		return nil, err
	}
	if key.Kid != kidStr {
		return nil, fmt.Errorf("serverkeys: kid mismatch between current.kid (%s) and unsealed key (%s)", kidStr, key.Kid)
	}
	return key, nil
}

func writeKey(dir string, key *Key, sealer Sealer) error {
	privPEM, err := MarshalPrivatePEM(key.PrivateKey)
	if err != nil {
		return err
	}
	sealed, err := sealer.Seal(privPEM)
	if err != nil {
		return fmt.Errorf("serverkeys: seal: %w", err)
	}
	pubPEM, err := MarshalPublicPEM(key.PublicKey)
	if err != nil {
		return err
	}
	sealedPath := filepath.Join(dir, key.Kid+sealedSuffix)
	pubPath := filepath.Join(dir, key.Kid+pubSuffix)
	if err := atomicWrite(sealedPath, sealed, 0o600); err != nil {
		return err
	}
	if err := atomicWrite(pubPath, pubPEM, 0o644); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, currentKid), []byte(key.Kid+"\n"), 0o644); err != nil {
		return err
	}
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func checkExpectedKid(dir, observed string, allowNew bool) error {
	data, err := os.ReadFile(filepath.Join(dir, expectedKid))
	if errors.Is(err, os.ErrNotExist) {
		// No sentinel yet — write one now to lock in the observed
		// kid for future boots.
		return writeExpectedKid(dir, observed)
	}
	if err != nil {
		return err
	}
	expected := strings.TrimSpace(string(data))
	if expected == observed {
		return nil
	}
	if allowNew {
		return writeExpectedKid(dir, observed)
	}
	return fmt.Errorf("serverkeys: identity kid mismatch — expected %q on disk, but loaded %q. Refusing to start (pass --allow-new-identity to override)", expected, observed)
}

func expectedKidExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, expectedKid))
	return err == nil
}

func writeExpectedKid(dir, kid string) error {
	return atomicWrite(filepath.Join(dir, expectedKid), []byte(kid+"\n"), 0o644)
}

// lockDir takes an exclusive file lock on `${dir}/.lock` and
// returns a release function. Cross-process race-free first-boot
// generation depends on this lock (AC edge case).
func lockDir(dir string) (func(), error) {
	path := filepath.Join(dir, ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("serverkeys: open lock: %w", err)
	}
	if err := flockEx(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("serverkeys: flock: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = flockUn(f)
			_ = f.Close()
		})
	}, nil
}
