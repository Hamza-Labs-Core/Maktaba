//go:build !windows

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/selfupdate"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// fakeReleases is one release whose payload tarball "verifies".
type fakeReleases struct {
	tag     string
	tarball string // packed onePayload
	fetched int
	refuse  error
}

func (f *fakeReleases) Latest(context.Context) (*selfupdate.Release, error) {
	return &selfupdate.Release{Tag: f.tag}, nil
}
func (f *fakeReleases) ByTag(_ context.Context, v string) (*selfupdate.Release, error) {
	if v != f.tag {
		return nil, selfupdate.ErrNoRelease
	}
	return &selfupdate.Release{Tag: f.tag}, nil
}
func (f *fakeReleases) FetchAsset(_ context.Context, rel *selfupdate.Release, name, dir string) (string, *releaseverify.Result, error) {
	f.fetched++
	if f.refuse != nil {
		return "", nil, f.refuse
	}
	if name != appruntime.ReleaseAsset(rel.Tag) {
		return "", nil, errors.New("asked for " + name)
	}
	b, err := os.ReadFile(f.tarball)
	if err != nil {
		return "", nil, err
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", nil, err
	}
	return p, &releaseverify.Result{Manifest: &releaseverify.Manifest{Version: strings.TrimPrefix(rel.Tag, "v")},
		Identity: "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/" + rel.Tag,
		SignedAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)}, nil
}

// packPayload packs a payload directory under server/, as release.yml does.
func packPayload(t *testing.T, payload string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.Walk(payload, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(payload, p)
		b, _ := os.ReadFile(p)
		if err := tw.WriteHeader(&tar.Header{Name: "server/" + filepath.ToSlash(rel), Typeflag: tar.TypeReg,
			Mode: int64(info.Mode().Perm()), Size: int64(len(b))}); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	out := filepath.Join(t.TempDir(), "payload.tar.gz")
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRuntimeUpdate(t *testing.T) {
	svc := &recordingService{}
	rt := appruntime.New(filepath.Join(t.TempDir(), "runtime"))
	rt.Sync = func() {}
	ctx := context.Background()
	res, err := rt.Install(ctx, onePayload(t, "v0.3.0"), nil, appruntime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Base(res.Dir)
	d := runtimeDeps{rt: rt, service: svc}
	src := &fakeReleases{tag: "v0.4.0", tarball: packPayload(t, onePayload(t, "v0.4.0"))}
	var out bytes.Buffer

	// the app updates a runtime it owns
	if err := runRuntimeUpdate(ctx, &out, d, src, "", false, false); !errors.Is(err, errAppOwnsRuntime) || src.fetched != 0 {
		t.Fatalf("app-owned: %v (fetched %d)", err, src.fetched)
	}
	if err := rt.SetOwner(appruntime.OwnerHeadless); err != nil {
		t.Fatal(err)
	}

	// --check verifies and installs nothing
	if err := runRuntimeUpdate(ctx, &out, d, src, "", true, false); err != nil || !strings.Contains(out.String(), "Verified v0.4.0") {
		t.Fatalf("--check: %v\n%s", err, out.String())
	}
	if dirs, _ := rt.List(); len(dirs) != 1 {
		t.Errorf("--check installed %v", dirs)
	}

	// an update: a new runtime, the service switched to it
	out.Reset()
	if err := runRuntimeUpdate(ctx, &out, d, src, "", false, false); err != nil {
		t.Fatalf("update: %v\n%s", err, out.String())
	}
	st, _ := rt.Status("")
	if st.Version != "v0.4.0" || st.Previous != first || strings.Join(svc.log, ",") != "stop,start" {
		t.Errorf("after the update: %+v, service %v", st, svc.log)
	}
	ents, _ := os.ReadDir(rt.Root)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), appruntime.DownloadPrefix) {
			t.Errorf("download left: %s", e.Name())
		}
	}

	// up to date now
	out.Reset()
	if err := runRuntimeUpdate(ctx, &out, d, src, "", false, false); err != nil || !strings.Contains(out.String(), "up to date") {
		t.Errorf("again: %v %s", err, out.String())
	}

	// --rollback: the one that ran before
	out.Reset()
	if err := runRuntimeUpdate(ctx, &out, d, src, "", false, true); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if cur, _ := rt.Current(); cur != first {
		t.Errorf("after --rollback current is %s, want %s", cur, first)
	}

	// a release that fails verification changes nothing
	src.refuse = errors.New("signature: no")
	if err := runRuntimeUpdate(ctx, &out, d, src, "v0.4.0", false, false); err == nil || !strings.Contains(err.Error(), "refusing v0.4.0") {
		t.Errorf("a refused release: %v", err)
	}
	if cur, _ := rt.Current(); cur != first {
		t.Errorf("current moved to %s", cur)
	}
}

// The tarball must hold the payload of the release it was verified as.
func TestRuntimeUpdateChecksThePayloadVersion(t *testing.T) {
	rt := appruntime.New(filepath.Join(t.TempDir(), "runtime"))
	rt.Sync = func() {}
	if _, err := rt.Install(context.Background(), onePayload(t, "v0.3.0"), nil, appruntime.Options{}); err != nil {
		t.Fatal(err)
	}
	_ = rt.SetOwner(appruntime.OwnerHeadless)
	src := &fakeReleases{tag: "v0.5.0", tarball: packPayload(t, onePayload(t, "v0.4.0"))}
	var out bytes.Buffer
	err := runRuntimeUpdate(context.Background(), &out, runtimeDeps{rt: rt, service: &recordingService{}}, src, "", false, false)
	if !errors.Is(err, appruntime.ErrInvalid) || !strings.Contains(err.Error(), "v0.4.0") {
		t.Errorf("a tarball of another version: %v", err)
	}
}
