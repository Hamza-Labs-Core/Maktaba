package pgbundle

import (
	"strings"
	"testing"
)

// The pause of the machines' copies for an update's migrations: each role quoted, its
// sessions ended (never this one), the table syncs found by their slots' names.
func TestCopiesPauseSQL(t *testing.T) {
	roles := []string{"maktaba_rep_a", `odd"name`}
	if got := PauseCopiesSQL(roles); got != "ALTER ROLE \"maktaba_rep_a\" NOLOGIN;\nALTER ROLE \"odd\"\"name\" NOLOGIN;\n" {
		t.Fatalf("pause %q", got)
	}
	if got := ResumeCopiesSQL(roles); got != "ALTER ROLE \"maktaba_rep_a\" LOGIN;\nALTER ROLE \"odd\"\"name\" LOGIN;\n" {
		t.Fatalf("resume %q", got)
	}
	end := EndCopiesSQL(roles)
	for _, want := range []string{"pg_terminate_backend(pid)", "usename IN ('maktaba_rep_a', 'odd\"name')",
		"pid <> pg_backend_pid()"} {
		if !strings.Contains(end, want) {
			t.Errorf("end lacks %q: %s", want, end)
		}
	}
	if !strings.Contains(TableSyncsSQL, `'^pg_[0-9]+_sync_[0-9]+_[0-9]+$'`) || !strings.Contains(TableSyncsSQL, "'"+AppDatabase+"'") {
		t.Errorf("table syncs: %s", TableSyncsSQL)
	}
	// the machines' own roles only: never the app role, which is in the group too and which
	// the migrations log in as (the auto-update e2e locked them out when it was paused)
	for _, want := range []string{"'" + ReplicaGroup + "'", "rolcanlogin", `LIKE 'maktaba\_rep\_%'`,
		"NOT IN ('" + AppRole + "', '" + SuperUser + "')", "<> current_user"} {
		if !strings.Contains(ReplicaLoginsSQL, want) {
			t.Errorf("replica logins lack %q: %s", want, ReplicaLoginsSQL)
		}
	}
}
