package secrets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// Postgres only (they skip without MAKTABA_TEST_PG_URL): slot 0120 on the
// fully migrated schema, read as the machines' group role.

func pgKey(t *testing.T) *secrets.Key {
	t.Helper()
	b := make([]byte, secrets.KeySize)
	for i := range b {
		b[i] = byte(i + 11)
	}
	k, err := secrets.NewKey(b)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// asWorker runs fn on one connection switched to maktaba_worker (slot
// 0103's group role every machine's login role is a member of).
func asWorker(ctx context.Context, t *testing.T, s *secrets.DBStore, query string, args ...any) error {
	t.Helper()
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET ROLE maktaba_worker`); err != nil {
		t.Fatalf("set role maktaba_worker: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), `RESET ROLE`) }()
	var v any
	return conn.QueryRowContext(ctx, query, args...).Scan(&v)
}

func isDenied(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "42501"
}

// TC2: maktaba_worker SELECT -> permission denied (and no write either).
func TestSecretsNotReadableByWorker(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	s := &secrets.DBStore{DB: db, Key: pgKey(t)}
	if err := s.Put(ctx, "cf_api_token", []byte("tok"), ""); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, "cf_api_token"); err != nil || string(got) != "tok" {
		t.Fatalf("home reads its secret: %q %v", got, err)
	}
	for _, q := range []string{
		`SELECT ciphertext FROM home_secrets LIMIT 1`,
		`SELECT count(*) FROM home_secrets`,
		`DELETE FROM home_secrets RETURNING name`,
		`INSERT INTO home_secrets (name, ciphertext, nonce, key_id) VALUES ('x', '\x00', '\x00', 'k') RETURNING name`,
	} {
		if err := asWorker(ctx, t, s, q); !isDenied(err) {
			t.Errorf("%s as maktaba_worker: %v, want permission denied", q, err)
		}
	}
	// the role keeps its rights on an ordinary table (the grant is intact)
	if err := asWorker(ctx, t, s, `SELECT count(*) FROM extensions`); err != nil {
		t.Errorf("maktaba_worker reads extensions: %v", err)
	}
}

// TC6: a Plex token in extensions.settings moves to home_secrets once, the
// row no longer holds it (what a machine can read), and maktaba_worker
// cannot read the moved value.
func TestExtensionSecretsMovedToHomeSecretsPG(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	s := &secrets.DBStore{DB: db, Key: pgKey(t)}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO extensions (name, enabled, settings, updated_at)
		VALUES ('plex', true, '{"url":"http://plex:32400","token":"PLEX-SECRET"}', now())
		ON CONFLICT (name) DO UPDATE SET settings = EXCLUDED.settings`); err != nil {
		t.Fatal(err)
	}
	rep, err := secrets.MigrateExtensionSecrets(ctx, s, queues.ExtensionSecretFields())
	if err != nil || len(rep.Moved) != 1 {
		t.Fatalf("move: %+v %v", rep, err)
	}
	rep, err = secrets.MigrateExtensionSecrets(ctx, s, queues.ExtensionSecretFields())
	if err != nil || len(rep.Moved)+len(rep.Dropped) != 0 {
		t.Fatalf("second move: %+v %v", rep, err)
	}
	var tokenLeft bool
	if err := db.QueryRowContext(ctx, `SELECT settings ? 'token' FROM extensions WHERE name = 'plex'`).Scan(&tokenLeft); err != nil || tokenLeft {
		t.Fatalf("token still in extensions.settings: %v %v", tokenLeft, err)
	}
	if got, err := s.Get(ctx, secrets.ExtensionName("plex", "token")); err != nil || string(got) != "PLEX-SECRET" {
		t.Fatalf("moved token: %q %v", got, err)
	}
	if err := asWorker(ctx, t, s, `SELECT settings->>'token' FROM extensions WHERE name = 'plex'`); err != nil {
		t.Fatalf("worker reads extensions: %v", err)
	}
	if err := asWorker(ctx, t, s, `SELECT ciphertext FROM home_secrets WHERE name = 'ext.plex.token'`); !isDenied(err) {
		t.Errorf("worker reads the moved token: %v", err)
	}
}
