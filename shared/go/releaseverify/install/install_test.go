package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

const repo = "Hamza-Labs-Core/Maktaba"

var platform = runtime.GOOS + "/" + runtime.GOARCH

type world struct {
	ss *releaseverifytest.Sigstore
	tm root.TrustedMaterial
	id rv.Identity
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ss, err := releaseverifytest.New()
	if err != nil {
		t.Fatal(err)
	}
	tm, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := rv.NewIdentity(repo)
	return &world{ss: ss, tm: tm, id: id}
}

// serve exposes a fake release's assets as "mem://<name>" URLs.
func serve(r *releaseverifytest.FakeRelease) (rv.Downloader, map[string]string) {
	assets := map[string]string{}
	for n := range r.Files {
		assets[n] = "mem://" + n
	}
	get := func(_ context.Context, u string, _ int64) ([]byte, error) {
		b, ok := r.Files[strings.TrimPrefix(u, "mem://")]
		if !ok {
			return nil, fmt.Errorf("404 %s", u)
		}
		return b, nil
	}
	return get, assets
}

func (w *world) stage(t *testing.T, l layout.Layout, r *releaseverifytest.FakeRelease) (*Staged, error) {
	t.Helper()
	get, assets := serve(r)
	return Stage(context.Background(), l, get, rv.Input{Tag: r.Tag, Identity: w.id, TrustedMaterial: w.tm}, assets, platform)
}

func TestStageActivateAndRollback(t *testing.T) {
	w := newWorld(t)
	l := layout.New(t.TempDir())
	st, err := w.stage(t, l, w.ss.MustRelease(repo, "v1.5.0", platform))
	if err != nil {
		t.Fatal(err)
	}
	// every binary of the archive, the signed files and .verified
	for _, n := range []string{"maktaba-server", "maktaba-api", "maktaba-streaming", "maktaba-launcher"} {
		if _, err := os.Stat(filepath.Join(st.Dir, layout.Exe(n))); err != nil {
			t.Errorf("%s not staged: %v", n, err)
		}
	}
	if err := l.ValidateRelease("1.5.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := rv.VerifyDir(st.Dir, "v1.5.0", w.id, rv.InstalledRelease, platform, w.tm); err != nil {
		t.Fatalf("staged dir does not re-verify offline: %v", err)
	}
	if cur, _ := l.Current(); cur != "" {
		t.Fatal("Stage changed current")
	}
	if err := st.Activate(l, "run-1"); err != nil {
		t.Fatal(err)
	}
	if cur, _ := l.Current(); cur != "1.5.0" {
		t.Fatalf("current = %q", cur)
	}
	if l.Watermark() != "1.5.0" {
		t.Fatalf("watermark = %q", l.Watermark())
	}
	_ = l.Confirm()

	st2, err := w.stage(t, l, w.ss.MustRelease(repo, "v1.6.0", platform))
	if err != nil {
		t.Fatal(err)
	}
	if err := st2.Activate(l, "run-2"); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.ReadPending(); p == nil || p.Version != "1.6.0" || p.Previous != "1.5.0" {
		t.Fatalf("pending = %+v", p)
	}
	v, err := Rollback(l)
	if err != nil || v != "1.5.0" {
		t.Fatalf("rollback = %q, %v", v, err)
	}
	if l.Watermark() != "1.6.0" {
		t.Fatal("a rollback lowered the watermark")
	}
	// a release older than the watermark is refused, before anything is written
	_, err = w.stage(t, l, w.ss.MustRelease(repo, "v1.5.5", platform))
	var e *rv.Error
	if !errors.As(err, &e) || e.Kind != rv.ErrDowngrade {
		t.Fatalf("downgrade: %v", err)
	}
	if _, err := os.Stat(l.ReleaseDir("1.5.5")); err == nil {
		t.Fatal("a refused release was staged")
	}
}

func TestStageRefusesTamperedArchive(t *testing.T) {
	w := newWorld(t)
	l := layout.New(t.TempDir())
	r := w.ss.MustRelease(repo, "v1.5.0", platform)
	r.Files[r.Archive] = append([]byte(nil), r.Files[r.Archive]...)
	r.Files[r.Archive][len(r.Files[r.Archive])/2] ^= 0xff
	if _, err := w.stage(t, l, r); err == nil {
		t.Fatal("a tampered archive was staged")
	}
	vs, _ := l.Versions()
	if len(vs) != 0 {
		t.Fatalf("left %v", vs)
	}
	ents, _ := os.ReadDir(l.ReleasesDir())
	for _, e := range ents {
		t.Errorf("left %s behind", e.Name())
	}
}

func TestStageRefusesOtherIdentity(t *testing.T) {
	w := newWorld(t)
	l := layout.New(t.TempDir())
	r := w.ss.MustRelease("someone/Maktaba", "v1.5.0", platform)
	if _, err := w.stage(t, l, r); err == nil {
		t.Fatal("a release signed by another repository was staged")
	}
}

func TestRootFindsLayout(t *testing.T) {
	t.Setenv(layout.EnvInstallRoot, "")
	l := layout.New(t.TempDir())
	if err := l.WritePointer(layout.CurrentFile, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	got, ok := Root(filepath.Join(l.ReleaseDir("1.5.0"), layout.Exe("maktaba-server")))
	if !ok || got.Root != l.Root {
		t.Fatalf("Root = %v %v", got, ok)
	}
	if _, ok := Root(filepath.Join(t.TempDir(), "maktaba-server")); ok {
		t.Fatal("a flat install was taken for a layout")
	}
	t.Setenv(layout.EnvInstallRoot, l.Root)
	if got, ok := Root(filepath.Join(t.TempDir(), "maktaba-api")); !ok || got.Root != l.Root {
		t.Fatal("MAKTABA_INSTALL_ROOT not honoured")
	}
}

// A release's migrations go beside its binaries (bin/releases/<v>/migrations), checked
// against checksums.txt; a tampered archive stages nothing.
func TestStageCarriesTheMigrations(t *testing.T) {
	w := newWorld(t)
	l := layout.New(t.TempDir())
	rel, err := w.ss.NewReleaseWith(repo, "v1.5.0", platform, releaseverifytest.ReleaseOptions{
		Migrations: map[string][]byte{
			"0001_init.sql": []byte("-- +goose Up\nCREATE TABLE a (id int);\n"), "0001_init.sqlite.sql": []byte("-- +goose Up\n"),
			"MANIFEST.md": []byte("# slots\n"),
		}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := w.stage(t, l, rel)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"0001_init.sql", "0001_init.sqlite.sql"} {
		if _, err := os.Stat(filepath.Join(st.Dir, MigrationsDir, n)); err != nil {
			t.Errorf("%s not staged: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(st.Dir, MigrationsDir, "MANIFEST.md")); err == nil {
		t.Error("MANIFEST.md staged among the migrations")
	}

	// the archive swapped for another after signing: refused, nothing staged
	l2 := layout.New(t.TempDir())
	name := "maktaba-migrations-1.5.0.tar.gz"
	rel.Files[name] = append([]byte(nil), rel.Files[name]...)
	rel.Files[name][len(rel.Files[name])-1] ^= 0xff
	if _, err := w.stage(t, l2, rel); !errors.Is(err, rv.ErrChecksum) {
		t.Fatalf("tampered migrations: %v", err)
	}
	if vs, _ := l2.Versions(); len(vs) != 0 {
		t.Fatalf("staged %v", vs)
	}
}

func TestExtractMigrationsRefusesOtherFiles(t *testing.T) {
	for name, entry := range map[string]string{"a path": "x/0001_a.sql", "a program": "evil.sh", "a climb": "../0001_a.sql"} {
		arch := tarGz(t, map[string]string{entry: "x"})
		if _, err := rv.ExtractMigrations(arch, t.TempDir()); err == nil {
			t.Errorf("%s: extracted", name)
		}
	}
}
