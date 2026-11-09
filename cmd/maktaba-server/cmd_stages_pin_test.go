package main

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components/testpg"
)

// Update rehearsal 16.7's race through the CLI: `stages impl promo_links inproc` on a stage
// still inproc from before the worker's first graph sync must hold, or that sync
// (pipeline engine/sync.py, _flip_first_party, the statement below) moves it to its plugin.
func TestStagesImplKeepsAnUnchangedChoiceThroughTheUpdateFlip(t *testing.T) {
	base := testpg.Get()
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset: a scratch Postgres whose user may create databases")
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "stagespin_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + name)
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// job_stages' impl columns as the home's schema has them
	if _, err := db.Exec(`CREATE TABLE job_stages (stage TEXT PRIMARY KEY, impl TEXT NOT NULL DEFAULT 'inproc',
		flipping BOOLEAN NOT NULL DEFAULT FALSE, impl_changed_at TIMESTAMPTZ, impl_changed_by TEXT);
		INSERT INTO job_stages (stage) VALUES ('promo_links')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var out bytes.Buffer
	if err := runStagesImpl(ctx, &out, db, "promo_links", "inproc", 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already runs inproc") {
		t.Fatalf("answered %q", out.String())
	}
	if _, err := db.Exec(`UPDATE job_stages SET impl = 'plugin' WHERE stage = ANY($1::text[])
		AND impl = 'inproc' AND impl_changed_at IS NULL`, "{promo_links}"); err != nil {
		t.Fatal(err)
	}
	var impl string
	var by sql.NullString
	if err := db.QueryRow(`SELECT impl, impl_changed_by FROM job_stages WHERE stage = 'promo_links'`).Scan(&impl, &by); err != nil {
		t.Fatal(err)
	}
	if impl != "inproc" || by.String != "cli" {
		t.Fatalf("after the update's flip promo_links runs %s (changed by %v), want inproc set by the CLI", impl, by)
	}
}
