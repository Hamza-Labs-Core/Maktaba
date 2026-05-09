package main

import (
	"strings"
	"testing"
)

func TestIdempotencyCatchesUnguardedCreateTable(t *testing.T) {
	stmt := "CREATE TABLE foo (id int)"
	vs := idempotencyCheck("0099_test.sql", stmt)
	if len(vs) == 0 {
		t.Fatal("expected violation for unguarded CREATE TABLE")
	}
	if vs[0].Rule != "idempotent-create-table" {
		t.Errorf("rule = %q, want idempotent-create-table", vs[0].Rule)
	}
}

func TestIdempotencyAcceptsGuarded(t *testing.T) {
	cases := []string{
		"CREATE TABLE IF NOT EXISTS foo (id int)",
		"CREATE INDEX IF NOT EXISTS idx ON foo(id)",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx ON foo(id)",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx ON foo(id)",
		"DROP TABLE IF EXISTS foo",
		"DROP INDEX IF EXISTS idx",
		"ALTER TABLE foo ADD COLUMN IF NOT EXISTS bar int",
	}
	for _, stmt := range cases {
		t.Run(stmt, func(t *testing.T) {
			if vs := idempotencyCheck("x.sql", stmt); len(vs) > 0 {
				t.Errorf("expected clean, got %d violations: %v", len(vs), vs)
			}
		})
	}
}

func TestIdempotencyCatchesEachKind(t *testing.T) {
	cases := map[string]string{
		"idempotent-create-table": "CREATE TABLE foo (id int)",
		"idempotent-create-index": "CREATE INDEX idx ON foo(id)",
		"idempotent-drop-table":   "DROP TABLE foo",
		"idempotent-drop-index":   "DROP INDEX idx",
		"idempotent-add-column":   "ALTER TABLE foo ADD COLUMN bar int",
	}
	for wantRule, stmt := range cases {
		t.Run(wantRule, func(t *testing.T) {
			vs := idempotencyCheck("x.sql", stmt)
			if len(vs) == 0 {
				t.Fatalf("expected violation, got none for %q", stmt)
			}
			found := false
			for _, v := range vs {
				if v.Rule == wantRule {
					found = true
				}
			}
			if !found {
				t.Errorf("did not see rule %s; saw: %v", wantRule, vs)
			}
		})
	}
}

func TestLongRunningCatchesPlainCreateIndex(t *testing.T) {
	stmt := "CREATE INDEX IF NOT EXISTS idx ON videos(name)"
	vs := longRunningCheck("0099_test.sql", stmt)
	if len(vs) == 0 {
		t.Fatal("expected violation for non-CONCURRENTLY CREATE INDEX")
	}
	if vs[0].Rule != "long-running-create-index" {
		t.Errorf("rule = %q, want long-running-create-index", vs[0].Rule)
	}
}

func TestLongRunningAcceptsConcurrently(t *testing.T) {
	stmt := "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx ON videos(name)"
	if vs := longRunningCheck("x.sql", stmt); len(vs) > 0 {
		t.Errorf("expected clean, got %v", vs)
	}
}

func TestLongRunningCatchesAlterType(t *testing.T) {
	stmt := "ALTER TABLE foo ALTER COLUMN bar TYPE bigint"
	vs := longRunningCheck("x.sql", stmt)
	if len(vs) == 0 || vs[0].Rule != "long-running-alter-type" {
		t.Fatalf("expected long-running-alter-type, got %v", vs)
	}
}

func TestLongRunningCatchesSetNotNull(t *testing.T) {
	stmt := "ALTER TABLE foo ALTER COLUMN bar SET NOT NULL"
	vs := longRunningCheck("x.sql", stmt)
	if len(vs) == 0 || vs[0].Rule != "long-running-set-not-null" {
		t.Fatalf("expected long-running-set-not-null, got %v", vs)
	}
}

func TestSplitStatementsHandlesGooseBlocks(t *testing.T) {
	body := []byte(`
CREATE TABLE IF NOT EXISTS a (id int);
CREATE TABLE IF NOT EXISTS b (id int);
INSERT INTO a (id) VALUES (1);
`)
	stmts := splitStatements(body)
	if len(stmts) != 3 {
		t.Fatalf("got %d stmts, want 3: %#v", len(stmts), stmts)
	}
}

func TestSplitStatementsIgnoresSemicolonInString(t *testing.T) {
	body := []byte(`INSERT INTO a (note) VALUES ('a;b'); SELECT 1;`)
	stmts := splitStatements(body)
	if len(stmts) != 2 {
		t.Fatalf("got %d stmts, want 2: %#v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "'a;b'") {
		t.Errorf("first stmt lost the embedded semicolon: %q", stmts[0])
	}
}

func TestStripCommentsRemovesGooseDirectives(t *testing.T) {
	body := []byte(`-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS foo (id int);
-- +goose StatementEnd`)
	stripped := stripComments(body)
	if strings.Contains(string(stripped), "+goose") {
		t.Errorf("goose directive survived: %q", stripped)
	}
	if !strings.Contains(string(stripped), "CREATE TABLE") {
		t.Errorf("CREATE TABLE got dropped: %q", stripped)
	}
}
