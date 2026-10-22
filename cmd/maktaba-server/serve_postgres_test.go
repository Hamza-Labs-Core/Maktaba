package main

import (
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// A home on a Postgres of its own is told at its start that its machines keep no copy of
// it without the rule, and what the rule would expose; a home on SQLite is told nothing.
func TestAHomeOnItsOwnPostgresIsWarned(t *testing.T) {
	var out strings.Builder
	cfg := config.Config{}
	cfg.Database.URL = "postgres://maktaba@db.example:5432/maktaba?sslmode=disable"
	warnOperatorPostgres(&out, cfg)
	for _, want := range []string{"rules.cluster.copies_on_operator_postgres", "test_decoding", "pg_signal_backend",
		"Remove those plugins"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("the warning does not say %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	cfg.Database.URL = "sqlite:///var/lib/maktaba/maktaba.db"
	warnOperatorPostgres(&out, cfg)
	if out.Len() != 0 {
		t.Fatalf("a home on SQLite was warned:\n%s", out.String())
	}
}
