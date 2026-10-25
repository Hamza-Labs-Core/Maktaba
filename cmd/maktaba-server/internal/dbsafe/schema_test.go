package dbsafe

import (
	"strings"
	"testing"
)

// The newest applied migration, from pg_restore's script of goose's table: by the COPY's
// column names, rows not applied left out; a dump without the table holds none.
func TestAppliedInScript(t *testing.T) {
	script := strings.Join([]string{
		"--",
		"-- PostgreSQL database dump",
		"SET statement_timeout = 0;",
		"COPY public.goose_db_version (id, version_id, is_applied, tstamp) FROM stdin;",
		"1\t0\tt\t2026-10-01 10:00:00",
		"2\t1\tt\t2026-10-01 10:00:00",
		"3\t149\tt\t2026-10-20 10:00:00",
		"4\t150\tf\t2026-10-21 10:00:00",
		"5\t148\tt\t2026-10-20 10:00:00",
		`\.`,
		"COPY public.other (version_id) FROM stdin;",
		"999",
	}, "\n")
	if got, err := appliedInScript(strings.NewReader(script)); err != nil || got != 149 {
		t.Fatalf("= %d %v, want 149", got, err)
	}
	reordered := "COPY public.goose_db_version (is_applied, version_id) FROM stdin;\nt\t12\nt\t13\n" + `\.` + "\n"
	if got, err := appliedInScript(strings.NewReader(reordered)); err != nil || got != 13 {
		t.Fatalf("columns in another order: %d %v", got, err)
	}
	if got, err := appliedInScript(strings.NewReader("SET x = 1;\n")); err != nil || got != 0 {
		t.Fatalf("no goose table: %d %v", got, err)
	}
	if _, err := appliedInScript(strings.NewReader("COPY public.goose_db_version (id, tstamp) FROM stdin;\n")); err == nil {
		t.Fatal("a COPY without version_id was read")
	}
}
