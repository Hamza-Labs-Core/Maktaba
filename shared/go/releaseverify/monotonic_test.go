package releaseverify_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

func verified(t *testing.T, f *fixture) *rv.Result {
	t.Helper()
	res, err := rv.Verify(f.input())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return res
}

func TestWatermarkDowngrade(t *testing.T) {
	f := newFixture(t)
	res := verified(t, f)
	err := rv.CheckMonotonic(rv.Watermark{Version: "1.6.0"}, res)
	wantKind(t, err, rv.ErrDowngrade)
	if err := rv.CheckMonotonic(rv.Watermark{Version: "1.4.2"}, res); err != nil {
		t.Fatalf("upgrade refused: %v", err)
	}
	if err := rv.CheckMonotonic(rv.Watermark{}, res); err != nil {
		t.Fatalf("first install refused: %v", err)
	}
}

func TestWatermarkReplaySameVersion(t *testing.T) {
	f := newFixture(t)
	res := verified(t, f)
	same := rv.Watermark{Version: "1.5.0", ChecksumsSHA256: res.ChecksumsSHA256, SignedAt: res.SignedAt}
	if err := rv.CheckMonotonic(same, res); err != nil {
		t.Fatalf("re-verifying the installed release: %v", err)
	}
	other := same
	other.ChecksumsSHA256 = strings.Repeat("f", 64)
	wantKind(t, rv.CheckMonotonic(other, res), rv.ErrReplay)
}

func TestReplayOlderIntegratedTime(t *testing.T) {
	f := newFixture(t)
	// v1.5.0 signed (logged) a week ago; the installed release was signed today.
	if err := f.rel.Resign(f.ss, func() releaseverifytest.SignOptions {
		o := releaseverifytest.GitHubRelease(repo, tag)
		o.SignedAt = time.Now().Add(-7 * 24 * time.Hour)
		return o
	}()); err != nil {
		t.Fatal(err)
	}
	res := verified(t, f)
	if time.Since(res.SignedAt) < 6*24*time.Hour {
		t.Fatalf("signed at %v, want a week ago (Rekor integrated time)", res.SignedAt)
	}
	hourAgo := time.Now().Add(-time.Hour)
	// an rc of this release, signed after it (tagged after the release): 1.5.0 is the
	// older build, passed by quietly
	wantKind(t, rv.CheckMonotonic(rv.Watermark{Version: "1.5.0-rc.7", SignedAt: hourAgo}, res), rv.ErrOlderBuild)
	// the same version signed again earlier: other content, a replay
	wantKind(t, rv.CheckMonotonic(rv.Watermark{Version: "1.5.0", SignedAt: hourAgo}, res), rv.ErrReplay)
	// a hotfix on an older minor signed after this one: 1.5.0 is a move up (story 22.5 EC1)
	if err := rv.CheckMonotonic(rv.Watermark{Version: "1.4.9", SignedAt: hourAgo}, res); err != nil {
		t.Fatalf("a hotfix line moving up to the next minor: %v", err)
	}
}

func TestWatermarkFile(t *testing.T) {
	f := newFixture(t)
	res := verified(t, f)
	wf := rv.WatermarkFile{Path: filepath.Join(t.TempDir(), "watermark")}
	w, err := wf.Load()
	if err != nil || w.Version != "" {
		t.Fatalf("missing file = %+v, %v", w, err)
	}
	if err := wf.Commit(res); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(wf.Path)
	if string(b) != "1.5.0\n" {
		t.Fatalf("watermark file = %q", b)
	}
	w, err = wf.Load()
	if err != nil || w.Version != "1.5.0" || w.ChecksumsSHA256 != res.ChecksumsSHA256 || !w.SignedAt.Equal(res.SignedAt) {
		t.Fatalf("loaded %+v, %v", w, err)
	}
	// Never lowered: committing an older release fails.
	old := *res
	m := *res.Manifest
	m.Version = "1.4.0"
	old.Manifest = &m
	wantKind(t, wf.Commit(&old), rv.ErrDowngrade)
	if w, _ := wf.Load(); w.Version != "1.5.0" {
		t.Fatalf("watermark lowered to %s", w.Version)
	}
	// A hand-written single-line watermark (design §2.1) still counts.
	if err := os.Remove(wf.Path + ".json"); err != nil {
		t.Fatal(err)
	}
	if w, _ := wf.Load(); w.Version != "1.5.0" || w.ChecksumsSHA256 != "" {
		t.Fatalf("text-only watermark = %+v", w)
	}
}
