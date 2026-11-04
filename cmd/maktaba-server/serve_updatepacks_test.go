package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
)

// Story 51.18: a release whose update fetched its packs before the switch builds the
// pipeline's venv from its own pack's wheels at its first start (no home's wheelhouse
// staged), and never deletes them; another release's pack is not used.
func TestVenvGateUsesTheReleasesPackWheels(t *testing.T) {
	root := filepath.Join(t.TempDir(), "components")
	l := packs.Layout{Root: root}
	mk := func(folder string) string {
		w := filepath.Join(root, "tool.yt-dlp", folder, "wheels")
		if err := os.MkdirAll(w, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w, "index.json"), []byte(`{"wheels":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		return w
	}
	old := mk("1.4.0_aaaaaaaaaaaa")
	if err := l.SetCurrent("tool.yt-dlp", "1.4.0_aaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	prev := venvRun
	t.Cleanup(func() { venvRun = prev })
	var got string
	venvRun = func(_ context.Context, spec appvenv.Spec, _ func(appvenv.Progress)) (appvenv.Result, error) {
		got = spec.Wheelhouse
		return appvenv.Result{Pipeline: "1.5.0"}, nil
	}
	missing := filepath.Join(t.TempDir(), "no-wheelhouse")
	g := &venvGate{spec: appvenv.Spec{Wheelhouse: missing}, version: "1.5.0", packs: l}
	if err := g.run(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if got != missing {
		t.Fatalf("another release's pack was used: %q", got)
	}
	cur := mk("1.5.0_bbbbbbbbbbbb")
	if err := l.SetCurrent("tool.yt-dlp", "1.5.0_bbbbbbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	if err := g.run(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if got != cur {
		t.Fatalf("the build was given %q, want the release's pack wheels %q", got, cur)
	}
	for _, w := range []string{old, cur} {
		if _, err := os.Stat(w); err != nil {
			t.Fatalf("a pack's wheels were deleted: %v", err)
		}
	}
}
