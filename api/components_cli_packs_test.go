package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	componentsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/components"
)

// fakePacks is the supervisor's pack installer as the CLI asks it.
type fakePacks struct {
	imports  []string
	importOK []string
	refuse   error
	made     []componentsh.OfflineRequest
	pending  int // FetchOffline answers progress this many times before the file
}

func (p *fakePacks) ProgressOf(_ context.Context, ids []string) map[string]*components.InstallProgress {
	out := map[string]*components.InstallProgress{}
	for _, id := range ids {
		if id == "runtime.ml" {
			out[id] = &components.InstallProgress{Phase: "installing", Percent: 42, Detail: "pack_downloading", Busy: true}
		}
	}
	return out
}

func (p *fakePacks) ImportPath(_ context.Context, path string) ([]string, error) {
	p.imports = append(p.imports, path)
	return p.importOK, p.refuse
}

func (p *fakePacks) MakeOffline(_ context.Context, req componentsh.OfflineRequest) (string, error) {
	p.made = append(p.made, req)
	return "maktaba-offline-1.2.3-linux-amd64-core.tar", p.refuse
}

func (p *fakePacks) FetchOffline(context.Context, string, http.Header) (*http.Response, *components.InstallProgress, error) {
	if p.pending > 0 {
		p.pending--
		return nil, &components.InstallProgress{Phase: "offline", Percent: 50, Busy: true}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("TARBYTES"))}, nil, nil
}

func offlineFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "home.tar")
	if err := os.WriteFile(p, []byte("tar"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// import hands the file's path to the supervisor and records what went in, as cli.
func TestComponentsCLIImport(t *testing.T) {
	f := &fakeComponents{}
	pk := &fakePacks{importOK: []string{"runtime.ml"}}
	c, out, _ := cli(f, false, "")
	c.packs = pk
	file := offlineFile(t)
	if code := runCLI(c, "import", file, "--yes"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(pk.imports) != 1 || pk.imports[0] != file || strings.Join(f.imported, ",") != "runtime.ml" ||
		f.actors[0] != (components.Actor{By: "maya", Source: "cli"}) || !strings.Contains(out.String(), "Installed: runtime.ml") {
		t.Fatalf("imports %v, recorded %v as %v: %s", pk.imports, f.imported, f.actors, out)
	}
}

// Without --yes and no terminal, import exits 2 and the supervisor is not asked; with no
// supervisor it exits 3; a refusal exits 1 and says what went in before it.
func TestComponentsCLIImportExits(t *testing.T) {
	file := offlineFile(t)
	pk := &fakePacks{}
	c, _, errOut := cli(&fakeComponents{}, false, "")
	c.packs = pk
	if code := runCLI(c, "import", file); code != exitComponentsNeedYes || len(pk.imports) != 0 || !strings.Contains(errOut.String(), "--yes") {
		t.Fatalf("no yes: exit %d, imports %v", code, pk.imports)
	}
	c, _, _ = cli(&fakeComponents{}, false, "")
	if code := runCLI(c, "import", file, "--yes"); code != exitComponentsNotRunning {
		t.Fatalf("no supervisor: exit %d", code)
	}
	c, _, _ = cli(&fakeComponents{}, false, "")
	c.packs = &fakePacks{refuse: errors.New("supervisor unreachable: connection refused")}
	if code := runCLI(c, "import", file, "--yes"); code != exitComponentsNotRunning {
		t.Fatalf("supervisor down: exit %d", code)
	}
	f := &fakeComponents{}
	c, _, errOut = cli(f, false, "")
	c.packs = &fakePacks{importOK: []string{"model.refine"}, refuse: &componentsh.SupError{Status: 409,
		Reason: "offline_wrong_version", Detail: "for 1.2.2", Installed: []string{"model.refine"}}}
	if code := runCLI(c, "import", file, "--yes"); code != exitComponentsFailed ||
		!strings.Contains(errOut.String(), "Refused (offline_wrong_version)") || !strings.Contains(errOut.String(), "model.refine") ||
		strings.Join(f.imported, ",") != "model.refine" {
		t.Fatalf("refused: %s, recorded %v", errOut, f.imported)
	}
	c, _, _ = cli(&fakeComponents{}, false, "")
	c.packs = &fakePacks{}
	if code := runCLI(c, "import", filepath.Join(t.TempDir(), "missing.tar"), "--yes"); code != exitComponentsFailed {
		t.Fatalf("missing file: exit %d", code)
	}
	if code := runCLI(c, "import"); code != exitComponentsFailed {
		t.Fatalf("no file: exit %d", code)
	}
}

// offline makes the file of a bundle, waits for it, saves it at --out and records both.
func TestComponentsCLIOffline(t *testing.T) {
	f := &fakeComponents{}
	pk := &fakePacks{pending: 2}
	c, out, errOut := cli(f, false, "")
	c.packs = pk
	dir := t.TempDir()
	if code := runCLI(c, "offline", "core", "--platform", "linux-amd64", "--out="+dir); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	saved := filepath.Join(dir, "maktaba-offline-1.2.3-linux-amd64-core.tar")
	if b, err := os.ReadFile(saved); err != nil || string(b) != "TARBYTES" {
		t.Fatalf("saved: %q %v", b, err)
	}
	if len(pk.made) != 1 || pk.made[0].Bundle != "core" || pk.made[0].Platform != "linux-amd64" ||
		strings.Join(f.records, ",") != components.ActionOfflineMake+","+components.ActionOfflineDownload ||
		!strings.Contains(errOut.String(), "offline 50%") || !strings.Contains(out.String(), "Saved") {
		t.Fatalf("made %+v, records %v: %s / %s", pk.made, f.records, out, errOut)
	}
	pk = &fakePacks{}
	c, _, _ = cli(&fakeComponents{}, false, "")
	c.packs = pk
	if code := runCLI(c, "offline", "runtime.ml", "model.refine", "--out", filepath.Join(dir, "two.tar")); code != 0 ||
		strings.Join(pk.made[0].Components, ",") != "runtime.ml,model.refine" {
		t.Fatalf("ids: %+v", pk.made)
	}
	c, _, _ = cli(&fakeComponents{}, false, "")
	if code := runCLI(c, "offline", "core"); code != exitComponentsNotRunning {
		t.Fatalf("no supervisor: exit %d", code)
	}
}

// show prints an asset's install progress (and --json carries it); a component that is
// no asset has none.
func TestComponentsCLIShowProgress(t *testing.T) {
	c, out, _ := cli(&fakeComponents{}, false, "")
	c.packs = &fakePacks{}
	if code := runCLI(c, "show", "runtime.ml"); code != 0 || !strings.Contains(out.String(), "on disk:    installing 42% (pack_downloading)") {
		t.Fatalf("exit %d: %s", code, out)
	}
	c, out, _ = cli(&fakeComponents{}, false, "")
	c.packs = &fakePacks{}
	if code := runCLI(c, "show", "runtime.ml", "--json"); code != 0 || !strings.Contains(out.String(), `"percent": 42`) {
		t.Fatalf("json: exit %d: %s", code, out)
	}
	c, out, _ = cli(&fakeComponents{}, false, "")
	c.packs = &fakePacks{}
	if code := runCLI(c, "show", "llm"); code != 0 || strings.Contains(out.String(), "on disk") {
		t.Fatalf("progress for no asset: %s", out)
	}
	if got := progressWords(&components.InstallProgress{Phase: "installing", Percent: 42, Detail: "pack_downloading", Busy: true}); got != "installing 42% (pack_downloading)" {
		t.Fatalf("words: %q", got)
	}
	if got := progressWords(&components.InstallProgress{Reason: "disk_space"}); got != "failed: disk_space" {
		t.Fatalf("words: %q", got)
	}
}

// --dsn, --out and --platform take values in either form; the values are no words.
func TestComponentsValueFlags(t *testing.T) {
	argv := []string{"offline", "--dsn", "postgres://x", "core", "--out=/tmp/x", "--platform", "darwin-arm64", "--yes"}
	flags, args := splitComponentsArgs(argv)
	v := componentsValues(argv)
	if strings.Join(args, " ") != "offline core" || !flags["yes"] || len(flags) != 1 ||
		v["dsn"] != "postgres://x" || v["out"] != "/tmp/x" || v["platform"] != "darwin-arm64" || componentsDSN(argv) != "postgres://x" {
		t.Fatalf("flags %v args %v values %v", flags, args, v)
	}
}
