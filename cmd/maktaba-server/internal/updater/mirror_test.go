package updater

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/install"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

// A node builds a plain server release's pipeline environment from its home's wheelhouse
// (design §3.4): fetched into the release's pipeline folder, handed to the build, and
// removed once the environment holds its packages. A home that keeps none, or a fetch
// that fails, leaves the build to PyPI (and says so).
func TestLayoutVenvFromTheHomesWheelhouse(t *testing.T) {
	rs := newReleases(t)
	ctx := context.Background()
	l := layout.New(t.TempDir())
	old, _ := rs.publish(t, "v1.4.0", releaseverifytest.ReleaseOptions{MinUpgradeFrom: "1.0.0"}, nil)
	get := func(_ context.Context, u string, _ int64) ([]byte, error) { return old.Files[filepath.Base(u)], nil }
	assetsOld := map[string]string{}
	for n := range old.Files {
		assetsOld[n] = "mem://" + n
	}
	if _, err := install.Stage(ctx, l, get, rv.Input{Tag: "v1.4.0", Identity: rs.source().Identity, TrustedMaterial: rs.tm},
		assetsOld, testPlatform); err != nil {
		t.Fatal(err)
	}
	if err := l.WritePointer(layout.CurrentFile, "1.4.0"); err != nil {
		t.Fatal(err)
	}
	_, assets := rs.publish(t, "v1.5.0", releaseverifytest.ReleaseOptions{MinUpgradeFrom: "1.4.0"}, nil)
	t.Setenv(envPipelinePython, "/opt/python3.12/bin/python3")

	type asked struct{ version, platform, dir string }
	var got []asked
	src := rs.source()
	answer := func(dir string) (bool, error) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
		return true, os.WriteFile(filepath.Join(dir, "index.json"), []byte(`{}`), 0o644)
	}
	src.Wheels = func(_ context.Context, version, platform, dir string) (bool, error) {
		got = append(got, asked{version, platform, dir})
		return answer(dir)
	}
	var built appvenv.Spec
	held := false
	prevBuild := venvBuild
	venvBuild = func(_ context.Context, spec appvenv.Spec, _ func(appvenv.Progress)) (appvenv.Result, error) {
		built = spec
		_, err := os.Stat(filepath.Join(spec.Wheelhouse, "index.json"))
		held = spec.Wheelhouse != "" && err == nil
		return appvenv.Result{}, os.MkdirAll(spec.Venv, 0o755)
	}
	t.Cleanup(func() { venvBuild = prevBuild })

	li := &LayoutInstaller{Layout: l, Source: src, Version: "1.4.0"}
	j := &Journal{Job: jobFor("v1.5.0", assets), From: "1.4.0"}
	var log bytes.Buffer
	if err := li.Stage(ctx, j, &log); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(l.ReleaseDir("1.5.0"), "pipeline", "wheelhouse")
	if len(got) != 1 || got[0].version != "1.5.0" || got[0].platform != testPlatform || got[0].dir != wantDir {
		t.Fatalf("the wheelhouse asked: %+v", got)
	}
	if built.Wheelhouse != wantDir || !held {
		t.Fatalf("the build was given %q (held %v)", built.Wheelhouse, held)
	}
	if _, err := os.Stat(wantDir); err == nil {
		t.Fatal("the wheelhouse was kept after the environment was built")
	}

	// none kept by the home, or a fetch that failed: the build goes to PyPI
	for _, c := range []struct {
		name string
		ok   bool
		err  error
	}{{"none kept", false, nil}, {"a failed fetch", false, errors.New("the home went away")}} {
		src.Wheels = func(context.Context, string, string, string) (bool, error) { return c.ok, c.err }
		li.Source = src
		_ = os.RemoveAll(l.ReleaseDir("1.5.0"))
		log.Reset()
		j := &Journal{Job: jobFor("v1.5.0", assets), From: "1.4.0"}
		if err := li.Stage(ctx, j, &log); err != nil {
			t.Fatal(err)
		}
		if built.Wheelhouse != "" {
			t.Fatalf("%s: the build was given %q", c.name, built.Wheelhouse)
		}
		if c.err != nil && !strings.Contains(log.String(), "the home went away") {
			t.Fatalf("%s: not logged: %s", c.name, log.String())
		}
	}
}

// A desktop app's or a runtime copy's staged release keeps the home's wheelhouse beside
// (the app's venv step builds from it at the release's first start): one release's only.
func TestStageWheelsKeepsTheStagedReleaseOnly(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"1.4.0", "1.3.0"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var into string
	src := Source{Wheels: func(_ context.Context, _, _, dir string) (bool, error) {
		into = dir
		return true, os.MkdirAll(dir, 0o755)
	}}
	j := &Journal{Job: Job{Version: "1.5.0"}}
	src.stageWheels(context.Background(), j, root, nil)
	if into != WheelhouseDir(root, "v1.5.0") {
		t.Fatalf("fetched into %q, want %q", into, WheelhouseDir(root, "1.5.0"))
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != filepath.Base(WheelhouseDir(root, "1.5.0")) {
		t.Fatalf("left %v", entries)
	}
	// no Wheels (the home, a machine of no home): nothing touched
	Source{}.stageWheels(context.Background(), j, root, nil)
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("a source with no wheelhouse touched %v", entries)
	}
}
