package pgbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pause of the machines' copies in pg_hba.conf: the rejects come first (the first line
// that matches decides), name the machines' replication roles alone, and go with the
// marker; a release that does not know the marker writes the file without them.
func TestPausedCopiesInHBA(t *testing.T) {
	b := Bundle{Data: t.TempDir(), LAN: true, TLS: true}
	if err := b.KeepReach(Reach{Open: true}); err != nil {
		t.Fatal(err)
	}
	open := b.hbaConf()
	if strings.Contains(open, "reject") {
		t.Fatalf("not paused, but rejects:\n%s", open)
	}
	if err := os.WriteFile(filepath.Join(b.Data, copiesPausedMarker), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !b.CopiesPaused() {
		t.Fatal("not paused with the marker there")
	}
	paused := b.hbaConf()
	var rules [][]string
	for _, l := range strings.Split(paused, "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			rules = append(rules, strings.Fields(l))
		}
	}
	want := []string{"local all /^maktaba_rep_ reject", "host all /^maktaba_rep_ all reject"}
	for i, w := range want {
		if got := strings.Join(rules[i], " "); got != w {
			t.Errorf("rule %d %q, want %q", i, got, w)
		}
	}
	for _, role := range []string{AppRole, SuperUser, "maktaba_node_0b6f3c1e"} {
		if strings.HasPrefix(role, strings.TrimPrefix(replicaRoles, "/^")) {
			t.Errorf("%s is kept out too", role)
		}
	}
	// the rest is as it was: nothing else is kept out
	if rest := paused[strings.Index(paused, "# This machine"):]; !strings.HasSuffix(open, rest) {
		t.Errorf("the other lines changed:\n%s", rest)
	}
	if err := os.Remove(filepath.Join(b.Data, copiesPausedMarker)); err != nil {
		t.Fatal(err)
	}
	if b.hbaConf() != open {
		t.Fatal("the file without the marker is not as before")
	}
}

// Only table syncs that stream now are counted: a slot left behind is permanent.
func TestTableSyncsSQL(t *testing.T) {
	for _, want := range []string{`'^pg_[0-9]+_sync_[0-9]+_[0-9]+$'`, "'" + AppDatabase + "'", " active "} {
		if !strings.Contains(TableSyncsSQL, want) {
			t.Errorf("table syncs lack %q: %s", want, TableSyncsSQL)
		}
	}
}
