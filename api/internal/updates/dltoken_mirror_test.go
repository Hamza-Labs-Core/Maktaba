package updates

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates/dltoken"
)

// A worker-only machine converting (story 35.8 AC8) has no delegation key: its download
// token lets it fetch the release it was given from the artifact route, for its machine
// while that machine is the cluster's, until it expires; never another release, never the
// wheelhouse or the TUF repository.
func TestMirrorTakesADownloadToken(t *testing.T) {
	t.Parallel()
	rig := newMirrorRig(t)
	if _, err := rig.store.DB.Exec(`INSERT INTO cluster_nodes (id, name, state, full_node) VALUES
		('w1', 'studio', 'approved', 0), ('w2', 'gone', 'revoked', 0)`); err != nil {
		t.Fatal(err)
	}
	archive := "maktaba-server_1.5.0_linux_amd64.tar.gz"
	path := "/api/cluster/updates/artifacts/1.5.0/" + archive
	bearer := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }
	tok, err := rig.m.DownloadToken("w1", "v1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if rec := rig.get(path, "", bearer(tok)); rec.Code != http.StatusOK || rec.Body.String() != string(rig.files[archive]) {
		t.Fatalf("the token's release: %d %q", rec.Code, rec.Body.String())
	}
	other, _ := rig.m.DownloadToken("w1", "1.4.0")
	revoked, _ := rig.m.DownloadToken("w2", "1.5.0")
	unknown, _ := rig.m.DownloadToken("w9", "1.5.0")
	forged := dltoken.Mint([]byte("not the home's key, not the home's"), "w1", "1.5.0", time.Now().Add(time.Hour))
	for name, bad := range map[string]string{"another release": other, "a revoked machine": revoked,
		"an unknown machine": unknown, "another key": forged, "garbage": dltoken.Prefix + "x.y"} {
		if rec := rig.get(path, "", bearer(bad)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	for _, p := range []string{"/api/cluster/updates/wheelhouse/1.5.0/linux-amd64",
		"/api/cluster/updates/wheelhouse/1.5.0/linux-amd64/anyio-4.4.0-py3-none-any.whl",
		"/api/cluster/updates/tuf/root.json"} {
		if rec := rig.get(p, "", bearer(tok)); rec.Code != http.StatusUnauthorized {
			t.Errorf("the token opened %s: %d", p, rec.Code)
		}
	}
	// expired
	rig.m.Now = func() time.Time { return time.Now().Add(dltoken.Lifetime + time.Minute) }
	if rec := rig.get(path, "", bearer(tok)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an expired token: %d", rec.Code)
	}
	rig.m.Now = nil
	// the key is kept: a restarted API takes the tokens it gave
	again := &Mirror{Store: rig.store, Dir: rig.m.Dir}
	key, err := again.key()
	if err != nil {
		t.Fatal(err)
	}
	if c, err := dltoken.Verify(key, tok, time.Now()); err != nil || c.Node != "w1" || c.Version != "1.5.0" {
		t.Fatalf("after a restart: %+v %v", c, err)
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(filepath.Join(rig.m.Dir, tokenKeyFile)); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("the key file: %v %v", st, err)
		}
	}
}
