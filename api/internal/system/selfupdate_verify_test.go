package system

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

// signedReleaseServer serves the GitHub releases API for repo with one release
// whose assets are rel's files.
func signedReleaseServer(t *testing.T, repo string, rel *releaseverifytest.FakeRelease) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/repos/"+repo+"/releases", func(w http.ResponseWriter, _ *http.Request) {
		var assets []ghAsset
		for name, b := range rel.Files {
			assets = append(assets, ghAsset{Name: name, URL: srv.URL + "/dl/" + name, Size: int64(len(b))})
		}
		_ = json.NewEncoder(w).Encode([]ghRelease{{TagName: rel.Tag, HTMLURL: "u", Assets: assets}})
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		b, ok := rel.Files[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
	return srv
}

type selfUpdateRig struct {
	su   *SelfUpdater
	self string
	rel  *releaseverifytest.FakeRelease
	exec chan string
}

// newRig: a host running v1.0.0 of the default repo, offered v1.5.0 by srvRepo
// signed as signerRepo.
func newRig(t *testing.T, signerRepo string) *selfUpdateRig {
	t.Helper()
	ss := releaseverifytest.MustNew()
	tr, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	rel := ss.MustRelease(signerRepo, "v1.5.0", releaseverify.Platform())
	srv := signedReleaseServer(t, defaultRepo, rel)
	u := NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0", APIBaseURL: srv.URL})
	su := NewSelfUpdater(u)
	su.trusted = func(context.Context) (root.TrustedMaterial, error) { return tr, nil }
	su.detect = func(string) installKind { return installBinary }
	self := filepath.Join(t.TempDir(), "maktaba-server")
	if err := os.WriteFile(self, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	su.selfPathFunc = func() (string, error) { return self, nil }
	rig := &selfUpdateRig{su: su, self: self, rel: rel, exec: make(chan string, 1)}
	su.execSelf = func(p string) error { rig.exec <- p; return nil }
	return rig
}

func (r *selfUpdateRig) post(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/system/update", strings.NewReader(`{"confirm":true}`)).
		WithContext(adminCtx())
	r.su.Handler().ServeHTTP(rec, req)
	return rec
}

func (r *selfUpdateRig) serverBinary() []byte {
	for n, b := range r.rel.Binaries {
		if strings.HasPrefix(n, "maktaba-server") {
			return b
		}
	}
	return nil
}

// TC8: POST /api/admin/system/update installs only what releaseverify accepted
// and raises the watermark.
func TestSelfUpdateUsesReleaseverify(t *testing.T) {
	rig := newRig(t, defaultRepo)
	rec := rig.post(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(rig.self); string(b) != string(rig.serverBinary()) {
		t.Fatalf("binary not replaced by the verified one: %q", b)
	}
	w, err := releaseverify.WatermarkFile{Path: WatermarkPath(rig.self)}.Load()
	if err != nil || w.Version != "1.5.0" || w.ChecksumsSHA256 == "" {
		t.Fatalf("watermark = %+v, %v", w, err)
	}
}

func TestSelfUpdateRefusesForkSignature(t *testing.T) {
	rig := newRig(t, "evil-fork/Maktaba")
	rec := rig.post(t)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "unexpected identity") {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(rig.self); string(b) != "OLD" {
		t.Fatalf("binary replaced despite a bad signature: %q", b)
	}
}

func TestSelfUpdateRefusesTamperedArchive(t *testing.T) {
	rig := newRig(t, defaultRepo)
	rig.rel.Files[rig.rel.Archive] = append(append([]byte{}, rig.rel.Files[rig.rel.Archive]...), 0)
	rec := rig.post(t)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "checksum mismatch") {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(rig.self); string(b) != "OLD" {
		t.Fatalf("binary replaced: %q", b)
	}
}

func TestSelfUpdateRefusesDowngrade(t *testing.T) {
	rig := newRig(t, defaultRepo)
	// This machine once ran 2.0.0 (then rolled back to the kept .bak).
	if err := os.WriteFile(WatermarkPath(rig.self), []byte("2.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := rig.post(t)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "downgrade") {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if b, _ := os.ReadFile(rig.self); string(b) != "OLD" {
		t.Fatalf("binary replaced: %q", b)
	}
}

func TestSelfUpdateBadRepoSettingFailsClosed(t *testing.T) {
	rig := newRig(t, defaultRepo)
	rig.su.u.idErr = os.ErrInvalid
	if rec := rig.post(t); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

// Without a RootStore the embedded root is still held to the age limit: an
// old binary that never refreshed fails closed instead of trusting stale keys.
func TestTrustedMaterialWithoutRootStoreChecksAge(t *testing.T) {
	emb, err := releaseverify.EmbeddedRootInfo()
	if err != nil {
		t.Fatal(err)
	}
	fresh := NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0",
		Now: func() time.Time { return emb.FetchedAt.Add(time.Hour) }})
	if _, err := fresh.trustedMaterial(context.Background()); err != nil {
		t.Fatalf("fresh embedded root refused: %v", err)
	}
	old := NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0",
		Now: func() time.Time { return emb.FetchedAt.Add(2 * releaseverify.DefaultRootMaxAge) }})
	if _, err := old.trustedMaterial(context.Background()); !errors.Is(err, releaseverify.ErrRootExpired) {
		t.Fatalf("expired embedded root: err = %v, want ErrRootExpired", err)
	}
}
