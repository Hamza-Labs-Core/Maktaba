package plugins

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// flowManifest is a one-stage plugin: it runs on trigger and emits <short>.done; a
// subscription to another plugin names it in [[depends]].
func flowManifest(id, trigger string, mayRequest ...string) []byte {
	short := id[strings.LastIndex(id, ".")+1:]
	src := `schema = 1
id = "` + id + `"
version = "1.0.0"
plugin_api = "1.0"
maktaba = ">=0.1.0"
licence = "MIT"

[strings]
name = "plugin.name"
summary = "plugin.summary"

[[stage]]
name = "work"
subject = "video"
export = "run-work"
version = 1
emits = ["` + short + `.done"]
`
	if len(mayRequest) > 0 {
		src += `may_request = ["` + strings.Join(mayRequest, `", "`) + `"]
`
	}
	src += `  [[stage.trigger]]
  event = "audio.ready"
`
	if trigger != "" {
		src += `  [[stage.trigger]]
  event = "` + trigger + `"
`
	}
	src += `
[[event]]
name = "` + short + `.done"
subject = "video"
payload = "events/done.schema.json"
`
	if publisher, ok := strings.CutPrefix(trigger, "plugin:"); ok {
		publisher, _, _ = strings.Cut(publisher, "/")
		src += `
[[depends]]
plugin = "` + publisher + `"
version = ">=1.0.0"
`
	}
	return []byte(src)
}

func (f *lcFixture) installFlow(t *testing.T, src []byte, enable bool) (*InstallResult, error) {
	t.Helper()
	return f.l.Install(context.Background(), InstallRequest{ConfirmName: "Lifecycle", Package: bytes.NewReader(src), Enable: enable}, admin)
}

func (f *lcFixture) enable(id string) error {
	_, err := f.l.Components.Apply(context.Background(), svc.Request{Changes: []svc.Change{{ID: components.PluginPrefix + id,
		Want: components.WantEnabled}}}, admin)
	return err
}

func flowProblem(t *testing.T, err error, code string) *Problem {
	t.Helper()
	p := asProblem(err)
	if p == nil {
		// an enable through the components service carries the hook's refusal in its text
		if err != nil && strings.Contains(err.Error(), code) {
			return &Problem{Code: code}
		}
		t.Fatalf("want %s, got %v (%T)", code, err, err)
	}
	if p.Code != code {
		t.Fatalf("want %s, got %s: %s", code, p.Code, p.Detail)
	}
	return p
}

const (
	fa = "dev.example.alpha"
	fb = "dev.example.beta"
	fc = "dev.example.gamma"
)

// 54.39 T03: two plugins that start each other. With one enabled, installing the other is
// refused with the path; with neither enabled, enabling the second is refused, and the
// first stays enabled.
func TestFlowCycleOfTwo(t *testing.T) {
	f := newLC(t)
	f.allowUnreviewed(t)
	if _, err := f.installFlow(t, flowManifest(fa, "plugin:"+fb+"/beta.done"), true); err != nil {
		t.Fatal(err)
	}
	_, err := f.installFlow(t, flowManifest(fb, "plugin:"+fa+"/alpha.done"), false)
	p := flowProblem(t, err, CodeFlowCycle)
	path, _ := p.Params["path"].([]string)
	want := []string{fa + "/work", "plugin:" + fa + "/alpha.done", fb + "/work", "plugin:" + fb + "/beta.done", fa + "/work"}
	if !slices.Equal(path, want) {
		t.Fatalf("path %v", path)
	}
	if ar, _ := p.Params["message_ar"].(string); !strings.Contains(ar, "حلقة") || !strings.Contains(ar, fa+"/work") {
		t.Fatalf("Arabic: %q", ar)
	}
	if n := f.count(t, `SELECT count(*) FROM plugin_installs WHERE plugin_id = $1`, fb); n != 0 {
		t.Fatal("the refused install was written")
	}
	// neither enabled: both install; the second enable is refused
	g := newLC(t)
	g.allowUnreviewed(t)
	for _, src := range [][]byte{flowManifest(fa, "plugin:"+fb+"/beta.done"), flowManifest(fb, "plugin:"+fa+"/alpha.done")} {
		if _, err := g.installFlow(t, src, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.enable(fa); err != nil {
		t.Fatal(err)
	}
	flowProblem(t, g.enable(fb), CodeFlowCycle)
	if !g.l.enabled(context.Background(), fa) || g.l.enabled(context.Background(), fb) {
		t.Fatal("the refused enable changed a plugin")
	}
}

// 54.39 T04: A starts B, B starts C (one flow), C starts A (another flow): each is fine
// alone; the third to be enabled is refused with the three-plugin path.
func TestFlowCycleOfThreeAcrossFlows(t *testing.T) {
	f := newLC(t)
	f.allowUnreviewed(t)
	for _, src := range [][]byte{flowManifest(fa, "plugin:"+fc+"/gamma.done"), flowManifest(fb, "plugin:"+fa+"/alpha.done")} {
		if _, err := f.installFlow(t, src, true); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.installFlow(t, flowManifest(fc, "plugin:"+fb+"/beta.done"), true)
	p := flowProblem(t, err, CodeFlowCycle)
	if path, _ := p.Params["path"].([]string); len(path) != 7 || path[0] != fa+"/work" {
		t.Fatalf("path %v", path)
	}
}

// 54.39 T05, T06: a subscriber whose upstream is missing installs and enables with a
// warning; with its upstream enabled, the upstream's downstream names its stage.
func TestFlowMissingUpstreamAndDownstream(t *testing.T) {
	f := newLC(t)
	f.allowUnreviewed(t)
	ctx := context.Background()
	res, err := f.installFlow(t, flowManifest(fb, "plugin:"+fa+"/alpha.done"), true)
	if err != nil || !res.Enabled {
		t.Fatalf("%+v %v", res, err)
	}
	g, err := f.l.Graph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Findings) != 1 || g.Findings[0].Code != "flow_missing_plugin" || g.Findings[0].Severity != "warn" ||
		!strings.Contains(g.Findings[0].MessageAr, fa) {
		t.Fatalf("findings %+v", g.Findings)
	}
	if _, err := f.installFlow(t, flowManifest(fa, ""), true); err != nil {
		t.Fatal(err)
	}
	d, err := f.l.Downstream(ctx, fa)
	if err != nil || !slices.Equal(d, []string{fb + "/work"}) {
		t.Fatalf("downstream %v %v", d, err)
	}
	g, _ = f.l.Graph(ctx)
	if len(g.Findings) != 0 {
		t.Fatalf("findings %+v", g.Findings)
	}
	// turned off, the upstream stays in the graph for the chart, marked installed
	if _, err := f.l.Components.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: components.PluginPrefix + fa,
		Want: components.WantInstalled}}}, admin); err != nil {
		t.Fatal(err)
	}
	g, _ = f.l.Graph(ctx)
	off := false
	for _, n := range g.Graph.Nodes {
		if n.Plugin == fa && n.State == "installed" {
			off = true
		}
	}
	if !off || len(g.Findings) != 1 {
		t.Fatalf("graph %+v findings %+v", g.Graph.Nodes, g.Findings)
	}
}

// a manifest that differs from the flow.toml in its own package is refused at install
func TestFlowManifestMustMatchItsFlow(t *testing.T) {
	f := newLC(t)
	f.allowUnreviewed(t)
	f.sup.flow = `schema = 1
id = "dev.example.flow"
version = "1.0.0"

[strings]
name = "flow.name"

[[plugin]]
id = "` + fa + `"
version = ">=1.0.0"

  [[plugin.stage]]
  name = "work"
  subject = "video"
  triggers = ["video.added"]
  emits = ["alpha.done"]

  [[plugin.event]]
  name = "alpha.done"
  subject = "video"
  payload = "events/done.schema.json"
`
	_, err := f.installFlow(t, flowManifest(fa, ""), false)
	p := flowProblem(t, err, CodeFlowInvalid)
	if !strings.Contains(p.Detail, "stage work triggers") {
		t.Fatal(p.Detail)
	}
	f.sup.flow = strings.Replace(f.sup.flow, `["video.added"]`, `["audio.ready"]`, 1)
	if _, err := f.installFlow(t, flowManifest(fa, ""), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.sup.root, fa)); err != nil {
		t.Fatal(err)
	}
}

// 54.41 T03 (API side): a plugin of a two-plugin flow installed alone: its flow names the
// other member as missing, and the chart's graph holds both, the other one marked missing.
func TestFlowViewNamesMissingMembers(t *testing.T) {
	f := newLC(t)
	f.allowUnreviewed(t)
	f.sup.flow = `schema = 1
id = "dev.example.flow"
version = "1.0.0"

[strings]
name = "flow.name"

[[plugin]]
id = "` + fa + `"
version = ">=1.0.0"

  [[plugin.stage]]
  name = "work"
  subject = "video"
  triggers = ["audio.ready"]
  emits = ["alpha.done"]

  [[plugin.event]]
  name = "alpha.done"
  subject = "video"
  payload = "events/done.schema.json"

[[plugin]]
id = "` + fb + `"
version = ">=1.0.0, <2.0.0"

  [[plugin.stage]]
  name = "work"
  subject = "video"
  triggers = ["audio.ready", "plugin:` + fa + `/alpha.done"]
  emits = ["beta.done"]

  [[plugin.event]]
  name = "beta.done"
  subject = "video"
  payload = "events/done.schema.json"
`
	if _, err := f.installFlow(t, flowManifest(fa, ""), false); err != nil {
		t.Fatal(err)
	}
	v, err := f.l.Flow(context.Background(), fa)
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != "dev.example.flow" || len(v.Missing) != 1 || v.Missing[0].ID != fb || v.Missing[0].Version != ">=1.0.0, <2.0.0" {
		t.Fatalf("view %+v", v)
	}
	states := map[string]string{}
	for _, n := range v.Graph.Nodes {
		states[n.Plugin] = n.State
	}
	if states[fa] != "installed" || states[fb] != "missing" {
		t.Fatalf("states %v", states)
	}
}
