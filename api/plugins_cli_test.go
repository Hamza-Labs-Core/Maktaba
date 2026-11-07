package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// fakePlugins records what the CLI asks of the lifecycle and the components service.
type fakePlugins struct {
	mu       sync.Mutex
	installs []plugins.InstallRequest
	digests  []string
	applied  []components.Change
	purged   []string
	grants   []plugins.GrantSet
	policy   plugins.Policy
	refuse   error
}

func (f *fakePlugins) List(context.Context) ([]plugins.View, error) {
	return []plugins.View{{ID: "dev.example.tagger", Version: "1.0.0", Want: "enabled", Phase: "running", ReviewLevel: "none"}}, nil
}

func (f *fakePlugins) Show(_ context.Context, id string) (*plugins.Detail, error) {
	if f.refuse != nil {
		return nil, f.refuse
	}
	d := &plugins.Detail{}
	d.ID, d.Version, d.Want, d.Phase = id, "1.0.0", "enabled", "running"
	d.Grants = plugins.GrantSet{Capabilities: []string{"tags.write"}, Libraries: []string{"lib-1"}}
	return d, nil
}

func (f *fakePlugins) Install(_ context.Context, req plugins.InstallRequest, _ components.Actor) (*plugins.InstallResult, error) {
	if f.refuse != nil {
		return nil, f.refuse
	}
	b, _ := io.ReadAll(req.Package)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs = append(f.installs, req)
	f.digests = append(f.digests, string(b[:min(len(b), 64)]))
	return &plugins.InstallResult{PluginID: "dev.example.notes", Version: "1.0.0", Updated: req.ExpectID != ""}, nil
}

func (f *fakePlugins) SetGrants(_ context.Context, _ string, g plugins.GrantSet, _ components.Actor) (*plugins.GrantSet, error) {
	f.grants = append(f.grants, g)
	return &g, nil
}

func (f *fakePlugins) Policy(context.Context) (plugins.Policy, error) { return f.policy, nil }

func (f *fakePlugins) SetPolicy(_ context.Context, p plugins.Policy, _ components.Actor) (plugins.Policy, error) {
	f.policy = p
	return p, nil
}

func (f *fakePlugins) Rollback(context.Context, string, bool, components.Actor) error {
	return f.refuse
}

func (f *fakePlugins) Apply(_ context.Context, req components.Request, _ components.Actor) (components.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, req.Changes...)
	return components.Result{}, nil
}

func (f *fakePlugins) Purge(_ context.Context, id string, _ components.Actor) (int, error) {
	f.purged = append(f.purged, id)
	return 3, nil
}

func pluginsRun(t *testing.T, f *fakePlugins, tty bool, in string, argv ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	flags, values, args := splitPluginsArgs(argv)
	c := &pluginsCLI{ops: f, in: strings.NewReader(in), out: &out, errOut: &errOut, tty: tty, user: "ci", values: values}
	return c.run(context.Background(), flags, args), out.String(), errOut.String()
}

// Story 54.10 §5 (#719): the CLI asks before a write, refuses one with no terminal and no
// --yes (2), and answers a refusal with 1 and its code.
func TestPluginsCLIWritesAskFirst(t *testing.T) {
	f := &fakePlugins{}
	pkg := filepath.Join(t.TempDir(), "p.mkplugin")
	_ = os.WriteFile(pkg, []byte("package"), 0o644)
	if code, _, errOut := pluginsRun(t, f, false, "", "install", pkg); code != exitComponentsNeedYes || !strings.Contains(errOut, "--yes") {
		t.Fatalf("no terminal: %d %q", code, errOut)
	}
	if code, out, _ := pluginsRun(t, f, true, "n\n", "install", pkg); code != exitComponentsFailed || !strings.Contains(out, "Nothing changed") {
		t.Fatalf("answered no: %d %q", code, out)
	}
	if len(f.installs) != 0 {
		t.Fatal("installed without a yes")
	}
	code, out, _ := pluginsRun(t, f, true, "y\n", "install", pkg, "--name", "Notes", "--enable", "--grant", "tags.write,media.read",
		"--library=lib-1")
	if code != 0 || !strings.Contains(out, "Install "+pkg) || !strings.Contains(out, "installed") {
		t.Fatalf("install: %d %q", code, out)
	}
	req := f.installs[0]
	if req.ConfirmName != "Notes" || !req.Enable || !req.GrantsSet || strings.Join(req.Grants, ",") != "tags.write,media.read" ||
		strings.Join(req.Libraries, ",") != "lib-1" {
		t.Fatalf("install request: %+v", req)
	}
	if code, _, _ := pluginsRun(t, f, false, "", "update", "dev.example.notes", pkg, "--yes"); code != 0 || f.installs[1].ExpectID != "dev.example.notes" {
		t.Fatalf("update: %d %+v", code, f.installs[1])
	}
	f.refuse = &plugins.Problem{Status: 409, Code: "plugin_unreviewed_disallowed", Detail: "allow unreviewed plugins first"}
	if code, _, errOut := pluginsRun(t, f, false, "", "install", pkg, "--yes"); code != exitComponentsFailed ||
		!strings.Contains(errOut, "plugin_unreviewed_disallowed") {
		t.Fatalf("a refusal: %d %q", code, errOut)
	}
}

// The switches, remove (and purge), grants and the policy call what the admin routes call.
func TestPluginsCLISwitchesRemoveGrantsPolicy(t *testing.T) {
	f := &fakePlugins{}
	cid := catalog.PluginPrefix + "dev.example.tagger"
	for _, tc := range []struct {
		action string
		check  func(components.Change) bool
	}{
		{"enable", func(c components.Change) bool { return c.Want == catalog.WantEnabled }},
		{"disable", func(c components.Change) bool { return c.Want == catalog.WantInstalled }},
		{"stop", func(c components.Change) bool { return c.Hold != nil && *c.Hold }},
		{"start", func(c components.Change) bool { return c.Hold != nil && !*c.Hold }},
	} {
		if code, _, _ := pluginsRun(t, f, false, "", tc.action, "dev.example.tagger", "--yes"); code != 0 {
			t.Fatalf("%s: %d", tc.action, code)
		}
		last := f.applied[len(f.applied)-1]
		if last.ID != cid || !tc.check(last) {
			t.Fatalf("%s applied %+v", tc.action, last)
		}
	}
	if code, out, _ := pluginsRun(t, f, false, "", "remove", "dev.example.tagger", "--purge", "--yes"); code != 0 ||
		f.applied[len(f.applied)-1].Want != catalog.WantAbsent || len(f.purged) != 1 || !strings.Contains(out, "3 writes reverted") {
		t.Fatalf("remove --purge: %d %q %v", code, out, f.purged)
	}
	if code, _, _ := pluginsRun(t, f, false, "", "grants", "dev.example.tagger", "--grant", "media.read", "--all-libraries", "--yes"); code != 0 {
		t.Fatal("grants")
	}
	if g := f.grants[0]; strings.Join(g.Capabilities, ",") != "media.read" || !g.AllLibraries || len(g.Libraries) != 0 {
		t.Fatalf("grants: %+v", g)
	}
	if code, out, _ := pluginsRun(t, f, false, "", "policy"); code != 0 || !strings.Contains(out, "false") {
		t.Fatalf("policy: %d %q", code, out)
	}
	if code, _, _ := pluginsRun(t, f, false, "", "policy", "--allow-unreviewed=on", "--yes"); code != 0 || !f.policy.AllowUnreviewed {
		t.Fatal("policy on")
	}
	if code, out, _ := pluginsRun(t, f, false, "", "list"); code != 0 || !strings.Contains(out, "dev.example.tagger") || !strings.Contains(out, "running") {
		t.Fatalf("list: %q", out)
	}
	if code, out, _ := pluginsRun(t, f, false, "", "show", "dev.example.tagger", "--json"); code != 0 || !strings.Contains(out, `"tags.write"`) {
		t.Fatalf("show --json: %q", out)
	}
}

func writePluginFolder(t *testing.T, dir, summary string) {
	t.Helper()
	files := map[string]string{
		"manifest.toml": `schema = 1
id = "dev.example.notes"
version = "1.0.0"
plugin_api = "1.0"
maktaba = ">=0.0.1"
licence = "MIT"

[strings]
name = "plugin.name"
summary = "plugin.summary"

[[stage]]
name = "keep_notes"
subject = "catalog"
export = "run-keep-notes"
version = 1
`,
		"i18n/en.json": `{"plugin.name":"Notes","plugin.summary":"` + summary + `"}`,
		"i18n/ar.json": `{"plugin.name":"ملاحظات","plugin.summary":"ملخص"}`,
		"plugin.wasm":  "\x00asm-not-really",
		".hidden":      "never packed",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Story 54.10 §4 (#719): developer mode installs the folder as Development, turned on, with
// its name; a save reinstalls it (an update of the same plugin); stopping removes it, unless
// --keep; and it refuses to start while unreviewed plugins are not allowed.
func TestPluginsDevMode(t *testing.T) {
	dir := t.TempDir()
	writePluginFolder(t, dir, "First.")
	f := &fakePlugins{}
	if code, _, errOut := pluginsRun(t, f, false, "", "dev", dir); code != exitComponentsFailed || !strings.Contains(errOut, "allow-unreviewed") {
		t.Fatalf("policy off: %d %q", code, errOut)
	}
	for _, keep := range []bool{false, true} {
		f = &fakePlugins{policy: plugins.Policy{AllowUnreviewed: true}}
		stop := make(chan struct{})
		var out bytes.Buffer
		c := &pluginsCLI{ops: f, in: strings.NewReader(""), out: &syncWriter{w: &out}, errOut: io.Discard, user: "ci",
			values: map[string]string{}, poll: 20 * time.Millisecond, stop: stop}
		done := make(chan int)
		go func() { done <- c.dev(context.Background(), dir, keep) }()
		waitUntil(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.installs) == 1 })
		first := f.installs[0]
		if first.ReviewLevel != "development" || !first.Enable || first.ConfirmName != "Notes" || first.ExpectID != "" {
			t.Fatalf("first load: %+v", first)
		}
		time.Sleep(60 * time.Millisecond) // nothing changed: no reload
		f.mu.Lock()
		n := len(f.installs)
		f.mu.Unlock()
		if n != 1 {
			t.Fatalf("reloaded with nothing changed: %d", n)
		}
		writePluginFolder(t, dir, "Second, after a save.")
		start := time.Now()
		waitUntil(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.installs) == 2 })
		if took := time.Since(start); took > 5*time.Second {
			t.Fatalf("the reload took %s (5 s at most)", took)
		}
		if f.installs[1].ExpectID != "dev.example.notes" {
			t.Fatalf("the reload: %+v", f.installs[1])
		}
		close(stop)
		if code := <-done; code != 0 {
			t.Fatalf("stop: %d", code)
		}
		removed := len(f.applied) == 1 && f.applied[0].Want == catalog.WantAbsent && f.applied[0].ID == catalog.PluginPrefix+"dev.example.notes"
		if removed == keep {
			t.Fatalf("keep %v: applied %+v", keep, f.applied)
		}
		writePluginFolder(t, dir, "First.")
	}
}

// The folder's package: every file but hidden ones, the plugin's id and English name.
func TestFolderPackage(t *testing.T) {
	dir := t.TempDir()
	writePluginFolder(t, dir, "First.")
	pkg, id, name, fingerprint, err := folderPackage(dir)
	if err != nil || len(pkg) == 0 || id != "dev.example.notes" || name != "Notes" || fingerprint == "" {
		t.Fatalf("%d %q %q %q %v", len(pkg), id, name, fingerprint, err)
	}
	if bytes.Contains(pkg, []byte("never packed")) {
		t.Fatal("a hidden file was packed")
	}
	_, _, _, again, _ := folderPackage(dir)
	writePluginFolder(t, dir, "Changed.")
	_, _, _, changed, _ := folderPackage(dir)
	if again != fingerprint || changed == fingerprint {
		t.Fatalf("fingerprints: %s %s %s", fingerprint, again, changed)
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
