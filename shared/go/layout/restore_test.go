package layout

import (
	"os"
	"path/filepath"
	"testing"
)

// A release says what its restore can do in its release.json, or in its migrations folder.
func TestReleaseRestoreProtocol(t *testing.T) {
	write := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		release, meta string
		want          int
	}{
		"nothing":               {},
		"from before":           {release: `{"v":1,"handover_protocol":1}`},
		"release.json":          {release: `{"v":1,"restore_protocol":1}`, want: 1},
		"an app's payload":      {meta: `{"protocol": 1, "about": "x"}`, want: 1},
		"neither can be read":   {release: `{`, meta: `[]`},
		"a release.json of old": {release: `{"v":1}`, meta: `{"protocol": 1}`, want: 1},
	} {
		dir := t.TempDir()
		if c.release != "" {
			write(t, filepath.Join(dir, "release.json"), c.release)
		}
		if c.meta != "" {
			write(t, filepath.Join(dir, "migrations", "meta", "restore.json"), c.meta)
		}
		if got := ReleaseRestoreProtocol(dir); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	// this one's own
	repo := t.TempDir()
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "meta", "restore.json"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "migrations", "meta", "restore.json"), string(b))
	if got := ReleaseRestoreProtocol(repo); got != RestoreProtocol {
		t.Errorf("meta/restore.json says %d, RestoreProtocol is %d", got, RestoreProtocol)
	}
}
