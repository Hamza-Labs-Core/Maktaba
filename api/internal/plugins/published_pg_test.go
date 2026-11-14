package plugins

import (
	"bytes"
	"context"
	"slices"
	"sort"
	"testing"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
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
	list, core, err := f.l.ListAll(context.Background(), true)
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
	// asked without the release's plugins (every client before story 54.44, the parental
	// rule pickers among them): the installed plugins only
	plain, _, err := f.l.ListAll(context.Background(), false)
	if err != nil || len(plain) != 1 || plain[0].ID != lcPlugin {
		t.Fatalf("the default list: %+v %v", plain, err)
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
	// the stages its stages hand work to, each with the plugin it belongs to: thumbnails'
	// small thumbnail feeds the review pages, which are Classify's
	th, err := f.l.Effect(ctx, "tv.maktaba.thumbnails")
	if err != nil {
		t.Fatal(err)
	}
	fed := false
	for _, x := range th.Fed {
		if x.Stage == "review_pages" && x.Plugin == "tv.maktaba.classify" && x.Label != "" {
			fed = true
		}
		if firstparty.ByStage(x.Stage) == nil || firstparty.ByStage(x.Stage).ID == "tv.maktaba.thumbnails" {
			t.Fatalf("thumbnails feeds %q, which is not another plugin's stage", x.Stage)
		}
	}
	if !fed {
		t.Fatalf("thumbnails' effect names no stage it feeds: %+v", th)
	}
	// a third-party stage that starts on an event one of its stages raises goes idle with
	// it (events.toml: transcript.ready is transcribe's): named once that plugin is on
	f.allowUnreviewed(t)
	follower := append(lcManifest("1.0.0", nil, nil, 1), []byte("  [[stage.trigger]]\n  event = \"transcript.ready\"\n")...)
	if _, err := f.l.Install(ctx, InstallRequest{ConfirmName: "Lifecycle", Package: bytes.NewReader(follower)}, admin); err != nil {
		t.Fatal(err)
	}
	if e, _ := f.l.Effect(ctx, "tv.maktaba.transcription"); len(e.Stages) != 0 {
		t.Fatalf("a plugin that is off is named as going idle: %+v", e.Stages)
	}
	if _, err := f.l.Components.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: "plugin." + lcPlugin, Want: components.WantEnabled}}}, admin); err != nil {
		t.Fatal(err)
	}
	if e, _ := f.l.Effect(ctx, "tv.maktaba.transcription"); len(e.Stages) != 1 || e.Stages[0] != lcPlugin+"/work" {
		t.Fatalf("the stage that follows transcription is not named: %+v", e)
	}
	// a plugin whose stages raise none of its events names none
	if e, _ := f.l.Effect(ctx, "tv.maktaba.subtitles"); len(e.Stages) != 0 {
		t.Fatalf("subtitles raises no event it follows: %+v", e.Stages)
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

// Story 54.44 (review): the other plugins the host runs are named by their own ids. An id
// is what follows "plugin."; stripping the characters of the prefix instead mangled every
// id that starts with p, l, u, g, i, n or a dot (plugin.io.example.x became o.example.x),
// so a plugin was listed as sharing the host with itself under a name no plugin has.
func TestHostSharersKeepTheirIDs(t *testing.T) {
	f := newLC(t)
	f.allowUnreviewed(t)
	ctx := context.Background()
	ids := []string{"io.example.x", "net.foo.bar", "plugin.example.p", "gui.lun.g", "lu.nip.u", "un.gil.n"}
	for _, id := range ids {
		pkg := bytes.ReplaceAll(lcManifest("1.0.0", nil, nil, 1), []byte(lcPlugin), []byte(id))
		if _, err := f.l.Install(ctx, InstallRequest{ConfirmName: "Lifecycle", Package: bytes.NewReader(pkg)}, admin); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if err := f.l.Components.Store.ReportStatus(ctx, store.Home, "supervisor", []store.Status{{ID: "plugins.host", Phase: "running",
		Detail: map[string]any{"rss_mb": 120}}}); err != nil {
		t.Fatal(err)
	}
	enable := func(id string) {
		t.Helper()
		if _, err := f.l.Components.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: "plugin." + id, Want: components.WantEnabled}}}, admin); err != nil {
			t.Fatalf("enable %s: %v", id, err)
		}
	}
	// alone in the host: it shares with no one
	enable(ids[0])
	d, err := f.l.Show(ctx, ids[0])
	if err != nil || len(d.Resources.Processes) != 1 || len(d.Resources.Processes[0].SharedWith) != 0 || d.Resources.Shared {
		t.Fatalf("a plugin alone in the host: %+v %v", d.Resources, err)
	}
	for _, id := range ids[1:] {
		enable(id)
	}
	for _, id := range ids {
		d, err := f.l.Show(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, o := range ids {
			if o != id {
				want = append(want, o)
			}
		}
		got := append([]string{}, d.Resources.Processes[0].SharedWith...)
		sort.Strings(got)
		sort.Strings(want)
		if !slices.Equal(got, want) || !d.Resources.Shared {
			t.Errorf("%s shares the host with %v, want %v", id, got, want)
		}
	}
	// the list says the same
	list, _, err := f.l.ListAll(ctx, false)
	if err != nil || len(list) != len(ids) {
		t.Fatalf("list: %d %v", len(list), err)
	}
	for _, v := range list {
		if slices.Contains(v.Resources.Processes[0].SharedWith, v.ID) || len(v.Resources.Processes[0].SharedWith) != len(ids)-1 {
			t.Errorf("%s in the list shares with %v", v.ID, v.Resources.Processes[0].SharedWith)
		}
	}
}
