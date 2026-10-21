// Package secrets is the home's sealed secret store (Epic 32 story 32.6,
// slot 0120 home_secrets).
//
// Values are sealed with AES-256-GCM under a 32-byte key read from a file
// in the home's config directory (secrets.key, mode 0600, created on first
// use). The key never enters the database: a database dump alone opens
// nothing, and the machines' group role (maktaba_worker) has no right on
// the table at all. The row name is the GCM additional data, so a sealed
// value cannot be moved to another name.
//
// Names in use:
//
//	cf_api_token        the Cloudflare API token (Stream Write)
//	cf_signing_key      the Stream signing key, JSON {id, pem}
//	ext.<name>.<field>  an extension's secret setting (ext.plex.token)
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// KeySize is the AES-256 key length.
const KeySize = 32

// KeyFileName is the key file's name inside the config directory.
const KeyFileName = "secrets.key"

var (
	// ErrNotFound: no secret by that name.
	ErrNotFound = errors.New("secrets: not found")
	// ErrWrongKey: the secret was sealed with another key (the key file
	// was replaced or lost); it must be entered again.
	ErrWrongKey = errors.New("secrets: sealed with another key, enter it again")
	// ErrNoKey: the store has no key (the key file could not be read or
	// created), so nothing can be sealed or opened.
	ErrNoKey = errors.New("secrets: no key file")
)

// Key is the sealing key and its short id (hex of sha256(key)[:8]).
type Key struct {
	b  []byte
	ID string
}

// NewKey wraps 32 raw bytes.
func NewKey(b []byte) (*Key, error) {
	if len(b) != KeySize {
		return nil, fmt.Errorf("secrets: key must be %d bytes, got %d", KeySize, len(b))
	}
	sum := sha256.Sum256(b)
	return &Key{b: append([]byte(nil), b...), ID: hex.EncodeToString(sum[:8])}, nil
}

// String never prints the key.
func (k *Key) String() string { return "secrets.Key(" + k.ID + ")" }

// KeyPath resolves the key file: MAKTABA_SECRETS_KEY, else the directory of
// MAKTABA_CONFIG (server.toml), else the platform keys directory
// maktaba-server uses (%LOCALAPPDATA%\Maktaba\keys on Windows,
// ~/Library/Application Support/Maktaba/keys on macOS, $XDG_CONFIG_HOME/maktaba
// or ~/.config/maktaba elsewhere). The pipeline (maktaba_pipeline.home_secrets)
// resolves it the same way. Under maktaba-server MAKTABA_SECRETS_KEY is always
// set; this is for the api run on its own.
func KeyPath(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if p := strings.TrimSpace(getenv("MAKTABA_SECRETS_KEY")); p != "" {
		return p
	}
	if p := strings.TrimSpace(getenv("MAKTABA_CONFIG")); p != "" {
		return filepath.Join(filepath.Dir(p), KeyFileName)
	}
	return filepath.Join(keysDir(getenv, runtime.GOOS), KeyFileName)
}

// keysDir mirrors maktaba-server's config.DirsFor(...).Keys.
func keysDir(getenv func(string) string, goos string) string {
	home, _ := os.UserHomeDir()
	switch goos {
	case "windows":
		local := getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(local, "Maktaba", "keys")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Maktaba", "keys")
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "maktaba")
	}
	return filepath.Join(home, ".config", "maktaba")
}

// LoadOrCreateKey reads the key file, or creates it (0600, random) when it
// does not exist. A file of the wrong size is an error, never overwritten:
// replacing it would make every stored secret unreadable.
func LoadOrCreateKey(path string) (*Key, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the configured key path
	if err == nil {
		if len(b) != KeySize {
			return nil, fmt.Errorf("secrets: %s is %d bytes, want %d (not replaced)", path, len(b), KeySize)
		}
		if st, serr := os.Stat(path); serr == nil && st.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(path, 0o600)
		}
		return NewKey(b)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("secrets: read key: %w", err)
	}
	raw := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return nil, fmt.Errorf("secrets: random key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("secrets: key dir: %w", err)
	}
	// O_EXCL: two processes starting together keep one key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // configured path
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadOrCreateKey(path)
		}
		return nil, fmt.Errorf("secrets: create key: %w", err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("secrets: write key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("secrets: close key: %w", err)
	}
	return NewKey(raw)
}

// Seal encrypts plain for name; it returns the ciphertext and a fresh
// random 12-byte nonce.
func Seal(k *Key, name string, plain []byte) (ciphertext, nonce []byte, err error) {
	if k == nil {
		return nil, nil, ErrNoKey
	}
	g, err := gcm(k)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, g.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("secrets: nonce: %w", err)
	}
	return g.Seal(nil, nonce, plain, []byte(name)), nonce, nil
}

// Open decrypts a value sealed for name. Any failure (another key, another
// name, a changed byte) is ErrWrongKey.
func Open(k *Key, name string, ciphertext, nonce []byte) ([]byte, error) {
	if k == nil {
		return nil, ErrNoKey
	}
	g, err := gcm(k)
	if err != nil {
		return nil, err
	}
	if len(nonce) != g.NonceSize() {
		return nil, ErrWrongKey
	}
	plain, err := g.Open(nil, nonce, ciphertext, []byte(name))
	if err != nil {
		return nil, ErrWrongKey
	}
	return plain, nil
}

func gcm(k *Key) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k.b)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Store is what the API's handlers use: the database-backed DBStore on the
// home, Memory in tests.
type Store interface {
	// Get opens a secret: ErrNotFound, ErrWrongKey or ErrNoKey on failure.
	Get(ctx context.Context, name string) ([]byte, error)
	// Put seals and stores a secret (insert or replace).
	Put(ctx context.Context, name string, value []byte, by string) error
	// Delete removes a secret; a missing one is not an error.
	Delete(ctx context.Context, name string) error
	// Has reports whether a row exists (it needs no key).
	Has(ctx context.Context, name string) (bool, error)
}

// DBStore keeps the secrets in home_secrets (Postgres placeholders; the
// modernc SQLite driver accepts them too).
type DBStore struct {
	DB  *sql.DB
	Key *Key // nil: Has/Delete work, Get/Put answer ErrNoKey
	Now func() time.Time
}

func (s *DBStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// Get implements Store.
func (s *DBStore) Get(ctx context.Context, name string) ([]byte, error) {
	var ct, nonce []byte
	var keyID string
	err := s.DB.QueryRowContext(ctx,
		`SELECT ciphertext, nonce, key_id FROM home_secrets WHERE name = $1`, name).Scan(&ct, &nonce, &keyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.Key == nil {
		return nil, ErrNoKey
	}
	if keyID != s.Key.ID {
		return nil, ErrWrongKey
	}
	return Open(s.Key, name, ct, nonce)
}

// Put implements Store.
func (s *DBStore) Put(ctx context.Context, name string, value []byte, by string) error {
	return s.put(ctx, s.DB, name, value, by)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *DBStore) put(ctx context.Context, q execer, name string, value []byte, by string) error {
	if s.Key == nil {
		return ErrNoKey
	}
	ct, nonce, err := Seal(s.Key, name, value)
	if err != nil {
		return err
	}
	var updatedBy any
	if by != "" {
		updatedBy = by
	}
	_, err = q.ExecContext(ctx, `
INSERT INTO home_secrets (name, ciphertext, nonce, key_id, updated_at, updated_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (name) DO UPDATE SET ciphertext = EXCLUDED.ciphertext, nonce = EXCLUDED.nonce,
    key_id = EXCLUDED.key_id, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`,
		name, ct, nonce, s.Key.ID, s.now(), updatedBy)
	return err
}

// Delete implements Store.
func (s *DBStore) Delete(ctx context.Context, name string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM home_secrets WHERE name = $1`, name)
	return err
}

// Has implements Store.
func (s *DBStore) Has(ctx context.Context, name string) (bool, error) {
	var one int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM home_secrets WHERE name = $1`, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Memory is an in-memory Store for tests (sealed like the real one).
type Memory struct {
	mu   sync.Mutex
	key  *Key
	rows map[string][2][]byte
	// Writes counts Put calls (tests).
	Writes int
}

// NewMemory returns an empty Memory store with a random key.
func NewMemory() *Memory {
	raw := make([]byte, KeySize)
	_, _ = io.ReadFull(rand.Reader, raw)
	k, _ := NewKey(raw)
	return &Memory{key: k, rows: map[string][2][]byte{}}
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[name]
	if !ok {
		return nil, ErrNotFound
	}
	return Open(m.key, name, r[0], r[1])
}

// Put implements Store.
func (m *Memory) Put(_ context.Context, name string, value []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ct, nonce, err := Seal(m.key, name, value)
	if err != nil {
		return err
	}
	m.rows[name] = [2][]byte{ct, nonce}
	m.Writes++
	return nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, name)
	return nil
}

// Has implements Store.
func (m *Memory) Has(_ context.Context, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rows[name]
	return ok, nil
}

// Corrupt replaces a stored value with one sealed under another key, as a
// lost key file would leave it (tests of EC3).
func (m *Memory) Corrupt(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[name]; ok {
		r[0] = append([]byte(nil), r[0]...)
		r[0][0] ^= 0xff
		m.rows[name] = r
	}
}

// ExtensionName is the home_secrets name of an extension's secret setting.
func ExtensionName(extension, field string) string {
	return "ext." + extension + "." + field
}
