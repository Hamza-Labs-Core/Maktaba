//go:build integration

package pgbundle

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// TestRealPgTextsearchPreloaded (Epic 49, story 49.1): a bundled cluster on a PostgreSQL 18
// install that has pg_textsearch (a payload's postgres-18/, MAKTABA_TEST_PG18_HOME) starts
// with the bundle's own arguments, which preload it: the superuser creates the extension,
// and a BM25 index on Maktaba's kind of text search configuration answers a ranked query.
// The payloads' check.sh runs the same on every platform's built payload
// (tools/release/pg18-check.sh); this one runs pgbundle's code.
func TestRealPgTextsearchPreloaded(t *testing.T) {
	home := os.Getenv("MAKTABA_TEST_PG18_HOME")
	if home == "" {
		t.Skip("MAKTABA_TEST_PG18_HOME unset (a PostgreSQL 18 install with pg_textsearch: a payload's postgres-18/)")
	}
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	root, err := os.MkdirTemp("", "mkpg18")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	b := Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "backups"), Port: testport.Free(t)}
	if got := b.Preload(); len(got) != 1 || got[0] != "pg_textsearch" {
		t.Fatalf("%s: Preload() = %v (no pg_textsearch in its lib/?)", home, got)
	}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if major, err := b.ClusterMajor(); err != nil || major != "18" {
		t.Fatalf("the cluster's major = %q, %v", major, err)
	}
	err = b.WithServer(ctx, io.Discard, func() error {
		super, err := b.SuperURL()
		if err != nil {
			return err
		}
		db, err := sql.Open("postgres", super)
		if err != nil {
			return err
		}
		defer db.Close()
		qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var preload string
		if err := db.QueryRowContext(qctx, "SHOW shared_preload_libraries").Scan(&preload); err != nil {
			return err
		}
		if !strings.Contains(preload, "pg_textsearch") {
			t.Errorf("shared_preload_libraries = %q", preload)
		}
		for _, q := range []string{
			"CREATE EXTENSION IF NOT EXISTS pg_textsearch",
			"CREATE TEXT SEARCH CONFIGURATION public.check_search (COPY = simple)",
			"ALTER TEXT SEARCH CONFIGURATION public.check_search ALTER MAPPING FOR hword, hword_part, word WITH unaccent, simple",
			"CREATE TABLE docs (id int PRIMARY KEY, body text NOT NULL)",
			"INSERT INTO docs SELECT i, CASE WHEN i = 3 THEN 'الصبر الصبر عند المصيبة' ELSE 'كلمة ' || i END FROM generate_series(1, 200) i",
			"CREATE INDEX docs_bm25 ON docs USING bm25 (body) WITH (text_config = 'public.check_search')",
		} {
			if _, err := db.ExecContext(qctx, q); err != nil {
				return err
			}
		}
		var top int
		if err := db.QueryRowContext(qctx,
			"SELECT id FROM docs ORDER BY body <@> to_bm25query('الصبر', 'docs_bm25') LIMIT 1").Scan(&top); err != nil {
			return err
		}
		if top != 3 {
			t.Errorf("the BM25 query ranks row %d first, not 3", top)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
