package secrets

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // pure-Go SQLite driver ("sqlite")
)

func testKey(t *testing.T, b byte) *Key {
	t.Helper()
	k, err := NewKey(bytes.Repeat([]byte{b}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TC1: round trip; wrong key -> error; nonce unique.
func TestSecretsSealOpen(t *testing.T) {
	k := testKey(t, 1)
	ct, nonce, err := Seal(k, "cf_api_token", []byte("hunter2"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("hunter2")) {
		t.Fatal("ciphertext holds the plaintext")
	}
	got, err := Open(k, "cf_api_token", ct, nonce)
	if err != nil || string(got) != "hunter2" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := Open(testKey(t, 2), "cf_api_token", ct, nonce); !errors.Is(err, ErrWrongKey) {
		t.Errorf("wrong key: %v", err)
	}
	if _, err := Open(k, "ext.plex.token", ct, nonce); !errors.Is(err, ErrWrongKey) {
		t.Errorf("a value moved to another name must not open: %v", err)
	}
	flipped := append([]byte(nil), ct...)
	flipped[0] ^= 1
	if _, err := Open(k, "cf_api_token", flipped, nonce); !errors.Is(err, ErrWrongKey) {
		t.Errorf("changed byte: %v", err)
	}
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		_, n, err := Seal(k, "x", []byte("same"))
		if err != nil {
			t.Fatal(err)
		}
		if len(n) != 12 || seen[string(n)] {
			t.Fatalf("nonce %d reused or wrong size", i)
		}
		seen[string(n)] = true
	}
	if _, _, err := Seal(nil, "x", nil); !errors.Is(err, ErrNoKey) {
		t.Errorf("nil key: %v", err)
	}
	if strings.Contains(k.String(), string(k.b)) || !strings.Contains(k.String(), k.ID) {
		t.Errorf("String() = %q", k.String())
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf", KeyFileName)
	k1, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() != KeySize {
		t.Fatalf("key file: %v %v", st, err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", st.Mode().Perm())
	}
	k2, err := LoadOrCreateKey(path)
	if err != nil || k2.ID != k1.ID {
		t.Fatalf("reload: %v %v / %v", k2, err, k1)
	}
	bad := filepath.Join(dir, "short.key")
	if err := os.WriteFile(bad, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(bad); err == nil {
		t.Error("a key file of the wrong size must be refused")
	}
	if b, _ := os.ReadFile(bad); string(b) != "short" {
		t.Error("a bad key file must never be replaced")
	}
}

func TestKeyPath(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	env["MAKTABA_SECRETS_KEY"] = "/x/secrets.key"
	if got := KeyPath(get); got != "/x/secrets.key" {
		t.Errorf("explicit: %s", got)
	}
	delete(env, "MAKTABA_SECRETS_KEY")
	env["MAKTABA_CONFIG"] = filepath.Join("cfg", "server.toml")
	if got := KeyPath(get); got != filepath.Join("cfg", KeyFileName) {
		t.Errorf("beside server.toml: %s", got)
	}
	delete(env, "MAKTABA_CONFIG")
	env["APPDATA"], env["XDG_CONFIG_HOME"] = "appdata", "xdg"
	want := filepath.Join("xdg", "maktaba", KeyFileName)
	if runtime.GOOS == "windows" {
		want = filepath.Join("appdata", "Maktaba", KeyFileName)
	}
	if got := KeyPath(get); got != want {
		t.Errorf("platform: %s, want %s", got, want)
	}
}

// sqliteDB has the two tables the store and the move touch.
func sqliteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "db", "migrations", "0120_home_secrets.sqlite.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up := strings.Split(string(b), "-- +goose Down")[0]
	for _, stmt := range []string{up,
		`CREATE TABLE extensions (name TEXT PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 0, settings TEXT NOT NULL DEFAULT '{}', updated_at TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	return db
}

func TestDBStore(t *testing.T) {
	ctx := context.Background()
	db := sqliteDB(t)
	s := &DBStore{DB: db, Key: testKey(t, 7)}
	if _, err := s.Get(ctx, "cf_api_token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.Put(ctx, "cf_api_token", []byte("v1"), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "cf_api_token", []byte("v2"), "u1"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, "cf_api_token"); err != nil || string(got) != "v2" {
		t.Fatalf("get = %q %v", got, err)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT ciphertext FROM home_secrets WHERE name = 'cf_api_token'`).Scan(&raw); err != nil || bytes.Contains(raw, []byte("v2")) {
		t.Fatalf("stored in clear or missing: %v", err)
	}
	// EC3: the key file was replaced; the secret is there but cannot open
	other := &DBStore{DB: db, Key: testKey(t, 8)}
	if _, err := other.Get(ctx, "cf_api_token"); !errors.Is(err, ErrWrongKey) {
		t.Errorf("replaced key: %v", err)
	}
	if ok, _ := other.Has(ctx, "cf_api_token"); !ok {
		t.Error("Has needs no key")
	}
	noKey := &DBStore{DB: db}
	if _, err := noKey.Get(ctx, "cf_api_token"); !errors.Is(err, ErrNoKey) {
		t.Errorf("no key: %v", err)
	}
	if err := noKey.Put(ctx, "x", []byte("y"), ""); !errors.Is(err, ErrNoKey) {
		t.Errorf("no key put: %v", err)
	}
	if err := s.Delete(ctx, "cf_api_token"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Has(ctx, "cf_api_token"); ok {
		t.Error("deleted secret still there")
	}
}

// TC6 (SQLite half; the Postgres half with the machines' role is in
// pg_test.go): a Plex token in extensions.settings moves once.
func TestExtensionSecretsMovedToHomeSecrets(t *testing.T) {
	ctx := context.Background()
	db := sqliteDB(t)
	s := &DBStore{DB: db, Key: testKey(t, 3)}
	if _, err := db.Exec(`INSERT INTO extensions (name, enabled, settings) VALUES
		('plex', 1, '{"url":"http://plex:32400","token":"PLEX-SECRET","path_map":"{}"}'),
		('dizquetv', 0, '{"url":"http://dq"}')`); err != nil {
		t.Fatal(err)
	}
	fields := ExtensionSecretFields{"plex": {"token": ExtensionName("plex", "token")}, "cloudflare_stream": {"api_token": "cf_api_token"}}
	rep, err := MigrateExtensionSecrets(ctx, s, fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Moved) != 1 || rep.Moved[0] != "ext.plex.token" {
		t.Fatalf("report %+v", rep)
	}
	var raw string
	if err := db.QueryRow(`SELECT settings FROM extensions WHERE name = 'plex'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	_ = json.Unmarshal([]byte(raw), &settings)
	if _, has := settings["token"]; has || settings["url"] != "http://plex:32400" || settings["path_map"] != "{}" {
		t.Fatalf("settings after the move: %s", raw)
	}
	if got, err := s.Get(ctx, "ext.plex.token"); err != nil || string(got) != "PLEX-SECRET" {
		t.Fatalf("moved token = %q %v", got, err)
	}
	// idempotent
	rep, err = MigrateExtensionSecrets(ctx, s, fields)
	if err != nil || len(rep.Moved)+len(rep.Dropped) != 0 {
		t.Fatalf("second run: %+v %v", rep, err)
	}
	// a stale copy written back by an old process is dropped; the newer
	// home_secrets value wins
	if _, err := db.Exec(`UPDATE extensions SET settings = '{"url":"http://plex:32400","token":"OLD"}' WHERE name = 'plex'`); err != nil {
		t.Fatal(err)
	}
	rep, err = MigrateExtensionSecrets(ctx, s, fields)
	if err != nil || len(rep.Dropped) != 1 {
		t.Fatalf("stale run: %+v %v", rep, err)
	}
	if got, _ := s.Get(ctx, "ext.plex.token"); string(got) != "PLEX-SECRET" {
		t.Errorf("stale copy overwrote the secret: %q", got)
	}
	_ = db.QueryRow(`SELECT settings FROM extensions WHERE name = 'plex'`).Scan(&raw)
	if strings.Contains(raw, "OLD") {
		t.Errorf("stale copy kept: %s", raw)
	}
}

func TestMemory(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.Put(ctx, "a", []byte("1"), "")
	if v, err := m.Get(ctx, "a"); err != nil || string(v) != "1" {
		t.Fatal(v, err)
	}
	m.Corrupt("a")
	if _, err := m.Get(ctx, "a"); !errors.Is(err, ErrWrongKey) {
		t.Errorf("corrupt: %v", err)
	}
}

// The pipeline (maktaba_pipeline.home_secrets) seals the same way: this
// vector was made with Python's cryptography AESGCM (key 0..31, nonce
// 100..111, name as the additional data) and is opened by both sides'
// tests.
func TestCrossLanguageVector(t *testing.T) {
	raw := make([]byte, KeySize)
	for i := range raw {
		raw[i] = byte(i)
	}
	k, _ := NewKey(raw)
	if k.ID != "630dcd2966c43366" {
		t.Fatalf("key id %s", k.ID)
	}
	ct, _ := hex.DecodeString("18579b3e54ba13dd6c270b6eee5432d33e9955c1a6f6d4333e9f9d")
	nonce, _ := hex.DecodeString("6465666768696a6b6c6d6e6f")
	got, err := Open(k, "ext.plex.token", ct, nonce)
	if err != nil || string(got) != "PLEX-SECRET" {
		t.Fatalf("open = %q %v", got, err)
	}
}
