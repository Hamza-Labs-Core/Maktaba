package plugins

import (
	"bytes"
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Story 54.44: one list of every plugin. A plugin an admin installed and the plugins
// Maktaba publishes are in it in the same shape, each with its publisher and source; what
// must always run is core and is not in it.
func TestListAllIsOneListAndCoreIsNoPlugin(t *testing.T) {
	f := newLC(t)
	f.allowUnreviewed(t)
	if _, err := f.l.Install(context.Background(), InstallRequest{ConfirmName: "Lifecycle",
		Package: bytes.NewReader(lcManifest("1.0.0", nil, nil, 1))}, admin); err != nil {
		t.Fatal(err)
	}
	list, core, err := f.l.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]View{}
	for i, v := range list {
		by[v.ID] = v
		if i > 0 && list[i-1].ID >= v.ID {
			t.Fatalf("the list is not by id: %s before %s", list[i-1].ID, v.ID)
		}
	}
	third, ok := by[lcPlugin]
	if !ok || third.Publisher.ID != "" || third.Source != SourceFile || third.Component != "plugin."+lcPlugin ||
		len(third.Queues) != 1 || third.Queues[0] != components.PluginQueueName(lcPlugin, "work") {
		t.Fatalf("the installed plugin: %+v", third)
	}
	sub, ok := by["tv.maktaba.subtitles"]
	if !ok || sub.Publisher != (Publisher{ID: PublisherMaktaba, Verified: true}) || sub.Source != SourceRelease ||
		sub.Component != "subtitles" || sub.Version != "0.3.83" || sub.Want != components.WantEnabled ||
		len(sub.Queues) != 1 || sub.Queues[0] != "subtitle_gen" || sub.Names.Name["ar"] == "" {
		t.Fatalf("a plugin Maktaba publishes: %+v", sub)
	}
	if _, listed := by["tv.maktaba.core-media"]; listed || IsPublished("tv.maktaba.core-media") {
		t.Fatal("core is listed as a plugin")
	}
	if len(core) != 1 || core[0].Component != "core.pipeline" || core[0].TitleKey == "" {
		t.Fatalf("core: %+v", core)
	}
	// every published plugin's component is switchable and names it back
	for _, fp := range published() {
		c, ok := components.Default().Get(fp.Trusted.Component)
		if !ok || !c.Switchable || c.Plugin != fp.ID {
			t.Errorf("%s: component %q switchable %v plugin %q", fp.ID, fp.Trusted.Component, ok && c.Switchable, c.Plugin)
		}
	}
	// a third-party plugin's detail carries its machines and the host it shares
	d, err := f.l.Show(context.Background(), lcPlugin)
	if err != nil || d.Machines == nil || d.Resources.Processes == nil || len(d.Stages) != 1 || d.Stages[0].RunsIn != "host" {
		t.Fatalf("the installed plugin's detail: %+v %v", d, err)
	}
	if p := asProblem(func() error { _, err := f.l.Show(context.Background(), "tv.maktaba.core-media"); return err }()); p == nil || p.Code != CodeNotInstalled {
		t.Fatalf("core has a plugin page: %+v", p)
	}
}

// Story 54.44: what turning a plugin off does, for both origins, and a Maktaba plugin's flow.
func TestEffectAndFlowOfAPublishedPlugin(t *testing.T) {
	f := newLC(t)
	ctx := context.Background()
	e, err := f.l.Effect(ctx, "tv.maktaba.livetv")
	if err != nil {
		t.Fatal(err)
	}
	slots := false
	for _, a := range e.Also {
		if a.Component == "livetv.slots" && a.Plugin == "tv.maktaba.livetv-slots" && a.To != "" {
			slots = true
		}
	}
	if !slots || len(e.Loops) == 0 || len(e.Features) != 1 || e.Features[0] != "live_tv" {
		t.Fatalf("livetv's effect: %+v", e)
	}
	if _, err := f.l.Effect(ctx, "dev.example.none"); asProblem(err) == nil || asProblem(err).Code != CodeNotInstalled {
		t.Fatalf("an unknown plugin's effect: %v", err)
	}
	fl, err := f.l.Flow(ctx, "tv.maktaba.thumbnails")
	if err != nil {
		t.Fatal(err)
	}
	mine, core := 0, 0
	for _, n := range fl.Graph.Nodes {
		switch {
		case n.Plugin == "tv.maktaba.thumbnails" && n.Kind == "stage":
			mine++
		case n.Kind == "core" && n.Plugin == "":
			core++
		case n.Plugin == "tv.maktaba.core-media":
			t.Fatalf("a core stage is drawn as a plugin's: %+v", n)
		}
	}
	if mine != len(firstparty.ByID("tv.maktaba.thumbnails").Stages) || len(fl.Graph.Edges) == 0 {
		t.Fatalf("thumbnails' flow: %d of its stages, %d edges, %d core", mine, len(fl.Graph.Edges), core)
	}
}

// Story 54.44: why a switch cannot be used, and how stages' numbers add up to a plugin's.
func TestLockAndActivity(t *testing.T) {
	for _, c := range []struct{ reason, held, want string }{
		{"", "", ""},
		{"dependency_off", "", ""}, // waiting on something else is no lock: the switch still works
		{"kept_off", "", ""},
		{"licence_missing", "", "licence_missing"},
		{"platform_unsupported", "", "platform_unsupported"},
		{"", "owner", "held"},
		{"licence_missing", "plugin-breaker", "held"},
	} {
		if got := lockOf(c.reason, c.held); got != c.want {
			t.Errorf("lockOf(%q, %q) = %q, want %q", c.reason, c.held, got, c.want)
		}
	}
	ms := func(n int64) *int64 { return &n }
	var a Activity
	a.add(Activity{Pending: 1, Running: 2, Failed24h: 1, Problems: 1, AvgMS: ms(1000), P95MS: ms(4000)}, 3)
	a.add(Activity{Pending: 4, AvgMS: ms(5000), P95MS: ms(9000)}, 1)
	a.add(Activity{Problems: 2}, 0)
	if a.Pending != 5 || a.Running != 2 || a.Done24h != 4 || a.Failed24h != 1 || a.Problems != 3 ||
		a.AvgMS == nil || *a.AvgMS != 2000 || a.P95MS == nil || *a.P95MS != 9000 {
		t.Fatalf("activity %+v avg %v p95 %v", a, a.AvgMS, a.P95MS)
	}
	if RefuseWithRelease("tv.maktaba.subtitles") == nil || RefuseWithRelease("dev.example.x") != nil ||
		RefuseWithRelease("tv.maktaba.core-media") != nil {
		t.Fatal("only a plugin of the release is refused its own update and removal")
	}
}
