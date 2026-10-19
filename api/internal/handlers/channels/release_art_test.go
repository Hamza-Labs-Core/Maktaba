package channels

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Story 33.11: the Changes page shows a staged release's art (?release=, admins only),
// served from the same art folders as the live picture.
func TestArtOfAStagedRelease(t *testing.T) {
	db, srv := newLogoServer(t)
	if _, err := db.Exec(`CREATE TABLE tv_release_channels (release_id TEXT NOT NULL, channel_id TEXT NOT NULL,
		def TEXT NOT NULL, art TEXT NOT NULL DEFAULT '{}', PRIMARY KEY (release_id, channel_id))`); err != nil {
		t.Fatal(err)
	}
	logos, elsewhere := t.TempDir(), t.TempDir()
	planLogoDir(t, db, logos)
	live := filepath.Join(logos, "live.png")
	staged := filepath.Join(logos, "staged.png")
	_ = os.WriteFile(live, []byte("\x89PNG live"), 0o600)
	_ = os.WriteFile(staged, []byte("\x89PNG staged"), 0o600)
	outside := filepath.Join(elsewhere, "x.png")
	_ = os.WriteFile(outside, []byte("\x89PNG x"), 0o600)
	const id = "00000000-0000-4000-8000-0000000000c1"
	insertChannel(t, db, id, live)
	def := func(p string) string {
		b, _ := json.Marshal(map[string]any{"id": id, "logo_path": p, "mode_config": map[string]any{}})
		return string(b)
	}
	if _, err := db.Exec(`INSERT INTO tv_release_channels (release_id, channel_id, def) VALUES ('r1', $1, $2), ('r2', $1, $3)`,
		id, def(staged), def(outside)); err != nil {
		t.Fatal(err)
	}
	get := func(path string, admin bool) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		if admin {
			req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u", IsAdmin: true}))
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, body := get("/api/channels/"+id+"/art/logo", false); code != 200 || body != "\x89PNG live" {
		t.Fatalf("live %d %q", code, body)
	}
	if code, body := get("/api/channels/"+id+"/art/logo?release=r1", true); code != 200 || body != "\x89PNG staged" {
		t.Fatalf("staged %d %q", code, body)
	}
	if code, _ := get("/api/channels/"+id+"/art/logo?release=r1", false); code != 403 {
		t.Fatalf("anonymous release art %d", code)
	}
	if code, _ := get("/api/channels/"+id+"/art/logo?release=r2", true); code != 404 {
		t.Fatalf("a release art outside the art folders %d", code)
	}
	if code, _ := get("/api/channels/"+id+"/art/logo?release=nope", true); code != 404 {
		t.Fatalf("unknown release %d", code)
	}
}
