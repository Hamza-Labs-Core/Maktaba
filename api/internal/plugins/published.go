package plugins

// Story 54.44: one Plugins page for every plugin.
//
// A plugin Maktaba publishes (an id under tv.maktaba., from this release's embedded
// bundle) is listed, shown and switched as any other plugin: the same View, with its
// publisher (Maktaba, verified) and where it came from (this release, until the store
// delivers it: stories 54.45 to 54.51). Its switch is its own: the plugin routes write the
// desired row of the Epic 51 component whose stages it implements (README §15.7), through
// the components service, so there is one source of truth for on and off and the
// Components page shows the same switch.
//
// What must always run for Maktaba to work is not a plugin: a first-party plugin whose
// component is not switchable (core.pipeline's tv.maktaba.core-media) is left out of the
// list, has no page and no switch, and is named only as part of Maktaba itself (Core).

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/lib/pq"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginflow"
)

// Who publishes a plugin, and where this home got it.
const (
	PublisherMaktaba = "maktaba"
	// SourceRelease: it came with this Maktaba release (it is updated with Maktaba and
	// cannot be removed until the store delivers it).
	SourceRelease = "release"
	SourceStore   = "store"
	SourceFile    = "file"
)

// CodeWithRelease refuses what a plugin that comes with the release cannot do yet: remove,
// update or roll back on its own, grants, schema settings, reverting writes (409).
const CodeWithRelease = "plugin_with_release"

// Switch locks: why a plugin's switch cannot be used (the reason's words are Epic 51's).
var switchLocks = map[string]bool{
	"licence_missing": true, "platform_unsupported": true, "env_override": true, "boot_flag": true,
	"reconcile_off": true, "incompatible": true, "revoked": true, "isolation_held": true,
	"plugin_trusted_unsigned": true, "plugin_trusted_version": true, "entitlement_missing": true,
	"unreviewed_disallowed": true,
}

// Publisher is who publishes a plugin: Maktaba (verified: the id prefix only this
// release's signed bundle can carry), or "" (someone else).
type Publisher struct {
	ID       string `json:"id"`
	Verified bool   `json:"verified"`
}

// Activity is a plugin's jobs over its stages: waiting and running now, done and failed in
// the last day, the failed jobs open now (Problems: what the failures view lists), and the
// mean and 95th percentile time of the last day's done jobs.
type Activity struct {
	Pending   int    `json:"pending"`
	Running   int    `json:"running"`
	Done24h   int    `json:"done_24h"`
	Failed24h int    `json:"failed_24h"`
	Problems  int    `json:"problems"`
	AvgMS     *int64 `json:"avg_ms,omitempty"`
	P95MS     *int64 `json:"p95_ms,omitempty"`
}

func (a *Activity) add(b Activity, done int) {
	a.Pending += b.Pending
	a.Running += b.Running
	a.Failed24h += b.Failed24h
	a.Problems += b.Problems
	// the mean of the stages' means, weighted by their jobs; the p95 is the slowest stage's
	if b.AvgMS != nil && done > 0 {
		total := int64(a.Done24h + done)
		avg := *b.AvgMS
		if a.AvgMS != nil {
			avg = (*a.AvgMS*int64(a.Done24h) + *b.AvgMS*int64(done)) / total
		}
		a.AvgMS = &avg
	}
	if b.P95MS != nil && (a.P95MS == nil || *b.P95MS > *a.P95MS) {
		p := *b.P95MS
		a.P95MS = &p
	}
	a.Done24h += done
}

// ProcessUse is one process that runs a plugin's stages, on one machine, and its memory
// as the machine's supervisor last measured it. A plugin runner group, the pipeline worker
// and the plugin host each serve several plugins: SharedWith names the others (SharedCore:
// Maktaba's own always-on stages run in it too), and the number is the whole process's,
// never a share of it.
type ProcessUse struct {
	// Process is "<role>/<instance>": plugin-runner/light, pipeline/, plugins/.
	Process string `json:"process"`
	// Group is the runner group (light, files, net, service, ml, index), "worker" for the
	// pipeline worker and "host" for the plugin host.
	Group     string `json:"group"`
	Component string `json:"component"`
	Machine   string `json:"machine"`
	Phase     string `json:"phase,omitempty"`
	RSSMB     *int64 `json:"rss_mb,omitempty"`
	// Scope is what RSSMB measures: "process" (this process alone) or "component" (every
	// process of Component together: its supervisor does not report each one yet).
	Scope string `json:"scope,omitempty"`
	// Stages are this plugin's stages that run in it.
	Stages     []string `json:"stages"`
	SharedWith []string `json:"shared_with"`
	SharedCore bool     `json:"shared_core"`
}

// Resources is what a plugin uses: the processes that run it, and what it stores.
// CPU is not here: no supervisor measures a process's CPU yet (story 54.52).
type Resources struct {
	Processes []ProcessUse `json:"processes"`
	// Shared: at least one of its processes also runs other plugins.
	Shared bool `json:"shared"`
	// DataRows, DataBytes and DataMB are a third-party plugin's own store and its quota
	// (story 54.09); MemoryMB its manifest's limit per job and PeakMemoryMB the most a
	// recent job used.
	DataRows     int64  `json:"data_rows"`
	DataBytes    int64  `json:"data_bytes"`
	DataMB       int    `json:"data_mb"`
	MemoryMB     int    `json:"memory_mb"`
	PeakMemoryMB *int64 `json:"peak_memory_mb,omitempty"`
}

// CoreView is a part of Maktaba itself that is not a plugin, whatever implements it.
type CoreView struct {
	Component string `json:"component"`
	TitleKey  string `json:"title_key"`
}

// TrustedCapability is one line of what a Maktaba-published plugin can do (story 54.22 §4).
type TrustedCapability struct {
	Key   string `json:"key"`
	Param string `json:"param"`
}

// IsPublished reports whether id is a plugin Maktaba publishes that this home lists (in
// this release's bundle, and not core).
func IsPublished(id string) bool {
	fp := firstparty.ByID(id)
	if fp == nil {
		return false
	}
	c, ok := components.Default().Get(fp.Trusted.Component)
	return ok && c.Switchable
}

// ComponentOf is the Epic 51 component a plugin's switch writes: a Maktaba-published
// plugin's own component, plugin.<id> for any other.
func ComponentOf(id string) string {
	if fp := firstparty.ByID(id); fp != nil && IsPublished(id) {
		return fp.Trusted.Component
	}
	return components.PluginPrefix + id
}

// Core answers the parts of Maktaba itself whose stages a first-party plugin implements:
// not switchable, so not plugins.
func Core() []CoreView {
	out := []CoreView{}
	list, _ := firstparty.Plugins()
	for _, fp := range list {
		if c, ok := components.Default().Get(fp.Trusted.Component); ok && !c.Switchable {
			out = append(out, CoreView{Component: c.ID, TitleKey: c.TitleKey})
		}
	}
	return out
}

// published is the list of Maktaba-published plugins, by id.
func published() []*firstparty.FirstParty {
	var out []*firstparty.FirstParty
	list, _ := firstparty.Plugins()
	for _, fp := range list {
		if IsPublished(fp.ID) {
			out = append(out, fp)
		}
	}
	return out
}

// fpNames are a first-party plugin's name and summary by language.
func fpNames(fp *firstparty.FirstParty) Names {
	strs, _ := fp.Manifest["strings"].(map[string]any)
	n := Names{Name: map[string]string{}, Summary: map[string]string{}}
	for _, lang := range []string{"en", "ar"} {
		k, _ := strs["name"].(string)
		n.Name[lang] = fp.Strings[lang][k]
		k, _ = strs["summary"].(string)
		n.Summary[lang] = fp.Strings[lang][k]
	}
	return n
}

// stageGroups maps each of a first-party plugin's stages to its runner group (its own
// `runner`, else the plugin's).
func stageGroups(fp *firstparty.FirstParty) map[string]string {
	out := map[string]string{}
	stages, _ := fp.Manifest["stage"].([]any)
	for _, s := range stages {
		st, _ := s.(map[string]any)
		name, _ := st["name"].(string)
		g, _ := st["runner"].(string)
		if g == "" {
			g = fp.Trusted.Runner
		}
		if name != "" {
			out[name] = g
		}
	}
	return out
}

// pageData is what one read of the list needs of the home, read once.
type pageData struct {
	comps    map[string]svc.View
	status   map[string][]store.Status // component -> its supervisor rows, by machine
	activity map[string]stageActivity  // stage (queue) -> its jobs
	impl     map[string]string         // stage -> inproc | plugin
}

type stageActivity struct {
	Activity
	done int
}

// loadPage reads the components, their status rows and the jobs of stages.
func (l *Lifecycle) loadPage(ctx context.Context, stages []string) (*pageData, error) {
	d := &pageData{comps: map[string]svc.View{}, status: map[string][]store.Status{},
		activity: map[string]stageActivity{}, impl: map[string]string{}}
	if l.Components != nil {
		lv, err := l.Components.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range lv.Components {
			d.comps[c.ID] = c
		}
		rows, err := l.Components.Store.Statuses(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Part == "supervisor" {
				d.status[r.ID] = append(d.status[r.ID], r)
			}
		}
	}
	if len(stages) == 0 || l.DB == nil {
		return d, nil
	}
	arg := pq.Array(stages)
	// three reads, each on its own index (0183's live and done indexes, the failed one)
	if err := l.each(ctx, `SELECT stage, count(*) FILTER (WHERE state = 'pending'),
		count(*) FILTER (WHERE state IN ('claimed', 'running', 'resuming'))
		FROM processing_jobs WHERE stage = ANY($1) AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused')
		GROUP BY stage`, arg, func(scan func(...any) error) error {
		var stage string
		var p, r int
		if err := scan(&stage, &p, &r); err != nil {
			return err
		}
		a := d.activity[stage]
		a.Pending, a.Running = p, r
		d.activity[stage] = a
		return nil
	}); err != nil {
		return nil, err
	}
	if err := l.each(ctx, `SELECT stage, count(*),
		avg(extract(epoch FROM finished_at - COALESCE(started_at, claimed_at)) * 1000)
			FILTER (WHERE COALESCE(started_at, claimed_at) IS NOT NULL),
		percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM finished_at - COALESCE(started_at, claimed_at)) * 1000)
			FILTER (WHERE COALESCE(started_at, claimed_at) IS NOT NULL)
		FROM processing_jobs WHERE stage = ANY($1) AND state = 'done' AND finished_at > now() - interval '1 day'
		GROUP BY stage`, arg, func(scan func(...any) error) error {
		var stage string
		var n int
		var avg, p95 *float64
		if err := scan(&stage, &n, &avg, &p95); err != nil {
			return err
		}
		a := d.activity[stage]
		a.Done24h, a.done = n, n
		a.AvgMS, a.P95MS = roundMS(avg), roundMS(p95)
		d.activity[stage] = a
		return nil
	}); err != nil {
		return nil, err
	}
	// a failed job is a problem while it is the subject's newest job of the stage (what the
	// failures view lists); one a later run replaced is only a failure of its day
	if err := l.each(ctx, `SELECT f.stage, count(*) FILTER (WHERE NOT EXISTS (SELECT 1 FROM processing_jobs n
			WHERE n.subject_kind = f.subject_kind AND n.subject_id = f.subject_id AND n.stage = f.stage AND n.id > f.id)),
		count(*) FILTER (WHERE f.finished_at > now() - interval '1 day')
		FROM processing_jobs f WHERE f.stage = ANY($1) AND f.state = 'failed' GROUP BY f.stage`, arg, func(scan func(...any) error) error {
		var stage string
		var open, day int
		if err := scan(&stage, &open, &day); err != nil {
			return err
		}
		a := d.activity[stage]
		a.Problems, a.Failed24h = open, day
		d.activity[stage] = a
		return nil
	}); err != nil {
		return nil, err
	}
	if err := l.each(ctx, `SELECT stage, impl FROM job_stages WHERE stage = ANY($1)`, arg, func(scan func(...any) error) error {
		var stage, impl string
		if err := scan(&stage, &impl); err != nil {
			return err
		}
		d.impl[stage] = impl
		return nil
	}); err != nil {
		return nil, err
	}
	return d, nil
}

func roundMS(f *float64) *int64 {
	if f == nil || *f < 0 {
		return nil
	}
	n := int64(*f + 0.5)
	return &n
}

func (l *Lifecycle) each(ctx context.Context, q string, arg any, row func(scan func(...any) error) error) error {
	rows, err := l.DB.QueryContext(ctx, q, arg)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := row(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

// activityOf sums the jobs of queues.
func (d *pageData) activityOf(queues []string) Activity {
	var a Activity
	for _, q := range queues {
		s := d.activity[q]
		a.add(s.Activity, s.done)
	}
	return a
}

// processesOf answers the rows of one process pattern of a component: one per machine
// that reports it, with the process's own memory when the supervisor reports each process
// (rss_parts), else the component's.
func (d *pageData) processesOf(component, role, instance, group string) []ProcessUse {
	var out []ProcessUse
	for _, r := range d.status[component] {
		u := ProcessUse{Process: role + "/" + instance, Group: group, Component: component, Machine: r.Machine,
			Phase: r.Phase, Stages: []string{}, SharedWith: []string{}}
		parts, _ := r.Detail["rss_parts"].(map[string]any)
		var own int64
		found := false
		for name, v := range parts {
			// the group's own process, and its numbered ones (ml-0, ml-1 …)
			if name != u.Process && !strings.HasPrefix(name, u.Process+"-") {
				continue
			}
			if n, ok := num(v); ok {
				own += n
				found = true
			}
		}
		switch {
		case found:
			u.RSSMB, u.Scope = &own, "process"
		case len(parts) == 0:
			if n, ok := num(r.Detail["rss_mb"]); ok {
				u.RSSMB, u.Scope = &n, "component"
				if c, ok := components.Default().Get(component); ok && len(c.Processes) == 1 && !strings.Contains(c.Processes[0], "*") {
					u.Scope = "process"
				}
			}
		}
		out = append(out, u)
	}
	return out
}

func num(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), x >= 0
	case int64:
		return x, x >= 0
	case int:
		return int64(x), x >= 0
	case json.Number:
		n, err := x.Int64()
		return n, err == nil && n >= 0
	}
	return 0, false
}

// publishedStages are every stage name of the Maktaba-published plugins (and core's, for
// the impl read that says what shares the worker).
func publishedStages() []string {
	var out []string
	list, _ := firstparty.Plugins()
	for _, fp := range list {
		out = append(out, fp.Stages...)
	}
	return out
}

// publishedView is one Maktaba-published plugin as the list shows it.
func (l *Lifecycle) publishedView(fp *firstparty.FirstParty, d *pageData) View {
	v := View{ID: fp.ID, Component: fp.Trusted.Component, Version: fp.Version, Digest: fp.Digest, Names: fpNames(fp),
		ReviewLevel: "reviewed", Publisher: Publisher{ID: PublisherMaktaba, Verified: true}, Source: SourceRelease,
		Grants: GrantSet{Capabilities: []string{}, Libraries: []string{}}, Required: []string{}, Optional: []string{},
		Health: Health{Recent: json.RawMessage("[]")}, Want: components.WantEnabled, StageNames: nonNil(fp.Stages), Queues: nonNil(fp.Stages),
		Settings: []string{}}
	// its version is the server's (README §15.6); a build with no version keeps the manifest's
	if l.Version != "" && l.Version != "unknown" {
		v.Version = l.Version
	}
	if c, ok := d.comps[v.Component]; ok {
		v.Want, v.Generation, v.Phase, v.Reason, v.ReasonParams = c.Want, c.Generation, c.Phase, c.Reason, c.ReasonParams
		v.Settings = nonNil(c.Settings)
		if c.Hold != nil {
			v.HeldBy = c.Hold.By
		}
		v.SwitchLocked = lockOf(c.Reason, v.HeldBy)
		if !c.Licensed {
			v.SwitchLocked = "licence_missing"
		}
	}
	v.Activity = d.activityOf(fp.Stages)
	v.Health.JobsDone, v.Health.JobsFailed = int64(v.Activity.Done24h), int64(v.Activity.Failed24h)
	v.Resources = l.publishedResources(fp, d)
	return v
}

// lockOf is why a switch cannot be used: a gate no switch opens, or a stop (Start lifts it).
func lockOf(reason, heldBy string) string {
	if heldBy != "" {
		return "held"
	}
	if switchLocks[reason] {
		return reason
	}
	return ""
}

// publishedResources is where a Maktaba-published plugin's stages run now: each stage in
// its runner group's process (job_stages.impl = plugin) or in the pipeline worker
// (inproc), with every other plugin that process runs.
func (l *Lifecycle) publishedResources(fp *firstparty.FirstParty, d *pageData) Resources {
	groups := stageGroups(fp)
	where := func(stage, group string) string {
		if d.impl[stage] == "plugin" {
			return group
		}
		return "worker"
	}
	mine := map[string][]string{}
	for _, s := range fp.Stages {
		g := where(s, groups[s])
		mine[g] = append(mine[g], s)
	}
	// who else runs in each of them
	others := map[string]map[string]bool{}
	core := map[string]bool{}
	list, _ := firstparty.Plugins()
	for _, o := range list {
		if o.ID == fp.ID {
			continue
		}
		og := stageGroups(o)
		for _, s := range o.Stages {
			g := where(s, og[s])
			if _, ok := mine[g]; !ok {
				continue
			}
			if !IsPublished(o.ID) {
				core[g] = true
				continue
			}
			if others[g] == nil {
				others[g] = map[string]bool{}
			}
			others[g][o.ID] = true
		}
	}
	// the worker also runs Maktaba's own always-on stages, whatever their impl
	core["worker"] = true
	res := Resources{Processes: []ProcessUse{}}
	names := make([]string, 0, len(mine))
	for g := range mine {
		names = append(names, g)
	}
	sort.Strings(names)
	for _, g := range names {
		role, instance, component := "plugin-runner", g, ""
		if g == "worker" {
			role, instance = "pipeline", ""
		}
		if cs := components.Default().MatchProcesses(role, instance); len(cs) > 0 {
			component = cs[0].ID
		}
		shared := make([]string, 0, len(others[g]))
		for id := range others[g] {
			shared = append(shared, id)
		}
		sort.Strings(shared)
		uses := d.processesOf(component, role, instance, g)
		if len(uses) == 0 {
			// no machine reports it yet: the process is still named, with no number
			uses = []ProcessUse{{Process: role + "/" + instance, Group: g, Component: component}}
		}
		for _, u := range uses {
			u.Stages, u.SharedWith, u.SharedCore = mine[g], shared, core[g]
			res.Processes = append(res.Processes, u)
			res.Shared = res.Shared || len(shared) > 0 || core[g]
		}
	}
	return res
}

// thirdPartyResources is the plugin host's process (every third-party plugin on a machine
// runs in it) and the plugin's own store.
func (l *Lifecycle) thirdPartyResources(v *View, d *pageData, enabled []string) Resources {
	res := Resources{Processes: []ProcessUse{}}
	shared := []string{}
	for _, id := range enabled {
		if id != v.ID {
			shared = append(shared, id)
		}
	}
	for _, u := range d.processesOf("plugins.host", "plugins", "", "host") {
		u.SharedWith = shared
		res.Processes = append(res.Processes, u)
	}
	res.Shared = len(shared) > 0 && len(res.Processes) > 0
	var recent []struct {
		Memory int64 `json:"memory"`
	}
	if json.Unmarshal(v.Health.Recent, &recent) == nil {
		var peak int64
		for _, r := range recent {
			if r.Memory > peak {
				peak = r.Memory
			}
		}
		if peak > 0 {
			mb := (peak + (1<<20 - 1)) >> 20
			res.PeakMemoryMB = &mb
		}
	}
	return res
}

// ListAll is every plugin of the home the Plugins page lists: the installed ones and the
// ones Maktaba publishes, in one list by id, and what is part of Maktaba itself.
func (l *Lifecycle) ListAll(ctx context.Context) ([]View, []CoreView, error) {
	installed, err := l.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	queues := map[string][]string{}
	stages := publishedStages()
	var enabled []string
	for i := range installed {
		v := &installed[i]
		for _, s := range v.StageNames {
			queues[v.ID] = append(queues[v.ID], components.PluginQueueName(v.ID, s))
		}
		stages = append(stages, queues[v.ID]...)
		if v.Want == components.WantEnabled {
			enabled = append(enabled, v.ID)
		}
	}
	d, err := l.loadPage(ctx, stages)
	if err != nil {
		return nil, nil, err
	}
	out := make([]View, 0, len(installed)+8)
	for i := range installed {
		v := installed[i]
		v.Activity = d.activityOf(queues[v.ID])
		v.Resources = l.thirdPartyResources(&v, d, enabled)
		out = append(out, v)
	}
	for _, fp := range published() {
		out = append(out, l.publishedView(fp, d))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, Core(), nil
}

// showPublished is GET /api/plugins/{id} for a plugin Maktaba publishes.
func (l *Lifecycle) showPublished(ctx context.Context, fp *firstparty.FirstParty) (*Detail, error) {
	d, err := l.loadPage(ctx, publishedStages())
	if err != nil {
		return nil, err
	}
	v := l.publishedView(fp, d)
	raw, _ := json.Marshal(map[string]any{"maktaba": fp.Manifest["maktaba"], "licence": fp.Manifest["licence"]})
	out := &Detail{View: v, Manifest: raw, Stages: []StageView{}, Capabilities: []TrustedCapability{}, Machines: []MachineState{}}
	groups := stageGroups(fp)
	subjects := map[string]string{}
	versions := map[string]int{}
	stages, _ := fp.Manifest["stage"].([]any)
	for _, s := range stages {
		st, _ := s.(map[string]any)
		name, _ := st["name"].(string)
		subjects[name], _ = st["subject"].(string)
		if n, ok := num(st["version"]); ok {
			versions[name] = int(n)
		}
	}
	for _, s := range fp.Stages {
		a := d.activity[s]
		runs := "worker"
		if d.impl[s] == "plugin" {
			runs = groups[s]
		}
		out.Stages = append(out.Stages, StageView{Name: s, Queue: s, Subject: subjects[s], Version: versions[s],
			Pending: a.Pending, Running: a.Running, Done24h: a.Done24h, Fail24h: a.Failed24h, Problems: a.Problems,
			AvgMS: a.AvgMS, P95MS: a.P95MS, RunsIn: runs})
	}
	for _, c := range fp.Capabilities() {
		out.Capabilities = append(out.Capabilities, TrustedCapability{Key: c["key"], Param: c["param"]})
	}
	if c, ok := d.comps[v.Component]; ok {
		out.Machines = machinesOf(c)
	}
	return out, nil
}

// MachineState is a plugin's status on one machine (Epic 51's, of its component).
type MachineState struct {
	Machine       string            `json:"machine"`
	Phase         string            `json:"phase"`
	Reason        string            `json:"reason,omitempty"`
	ReasonParams  map[string]string `json:"reason_params,omitempty"`
	Restarts24h   int               `json:"restarts_24h"`
	LastErrorCode string            `json:"last_error_code,omitempty"`
	Stale         bool              `json:"stale"`
}

func machinesOf(c svc.View) []MachineState {
	out := []MachineState{}
	for _, m := range c.Machines {
		s := MachineState{Machine: m.Machine, Phase: m.Phase, Reason: m.Reason, ReasonParams: m.ReasonParams}
		if m.Health != nil {
			s.Restarts24h, s.LastErrorCode, s.Stale = m.Health.Restarts24h, m.Health.LastErrorCode, m.Health.Stale
		}
		out = append(out, s)
	}
	return out
}

// SwitchEffect is what turning a plugin off does beside stopping the plugin itself
// (GET /api/plugins/{id}/downstream): the confirm dialog names each.
type SwitchEffect struct {
	// Stages are other plugins' stages that go idle (story 54.39 §6).
	Stages []string `json:"stages"`
	// Also are the other components the switch moves, each with the phase it moves to
	// (waiting: it needs this one; degraded: it runs with less) and the plugin it belongs to
	// when it has one.
	Also []AlsoOff `json:"also"`
	// Processes and Loops are the plugin's component's other parts: they stop with it.
	Processes []string `json:"processes"`
	Loops     []string `json:"loops"`
	// Features are the user-facing features that go with it (live_tv, transcripts …).
	Features         []string `json:"features"`
	QueuedJobsParked int      `json:"queued_jobs_parked"`
	DrainS           int      `json:"drain_s"`
}

// AlsoOff is one component a switch touches.
type AlsoOff struct {
	Component string `json:"component"`
	TitleKey  string `json:"title_key"`
	// Plugin is the plugin whose component it is ("" for a part of Maktaba).
	Plugin string `json:"plugin,omitempty"`
	To     string `json:"to,omitempty"`
}

func (l *Lifecycle) alsoOf(id, to string) AlsoOff {
	a := AlsoOff{Component: id, To: to}
	if c, ok := components.Default().Get(id); ok {
		a.TitleKey = c.TitleKey
		if c.Switchable {
			a.Plugin = c.Plugin
		}
	}
	if p, ok := strings.CutPrefix(id, components.PluginPrefix); ok {
		a.Plugin = p
	}
	return a
}

// Effect answers what turning plugin id off does (404 plugin_not_installed).
func (l *Lifecycle) Effect(ctx context.Context, id string) (*SwitchEffect, error) {
	out := &SwitchEffect{Stages: []string{}, Also: []AlsoOff{}, Processes: []string{}, Loops: []string{}, Features: []string{}}
	cid := ComponentOf(id)
	if !IsPublished(id) {
		in, err := loadInstall(ctx, l.DB, id, false)
		if err != nil {
			return nil, err
		}
		if in == nil {
			return nil, problem(http.StatusNotFound, CodeNotInstalled, "%s is not installed", id)
		}
		st, err := l.Downstream(ctx, id)
		if err != nil {
			return nil, err
		}
		out.Stages = st
	} else if c, ok := components.Default().Get(cid); ok {
		out.Processes, out.Loops, out.Features = nonNil(c.Processes), nonNil(c.Loops), nonNil(c.Features)
	}
	if l.Components == nil {
		return out, nil
	}
	p, err := l.Components.Preview(ctx, svc.Request{Changes: []svc.Change{{ID: cid, Want: components.WantInstalled}}})
	if err != nil {
		if _, ok := svc.AsRefusal(err); ok || err == svc.ErrUnknown {
			return out, nil
		}
		return nil, err
	}
	for _, e := range p.Effects {
		if e.ID != cid {
			out.Also = append(out.Also, l.alsoOf(e.ID, e.To))
		}
	}
	// what needs it and is on waits, whatever its phase is now (a dependant already waiting
	// on something else is no effect of the preview, and still stops being able to run)
	named := map[string]bool{}
	for _, a := range out.Also {
		named[a.Component] = true
	}
	if lv, err := l.Components.List(ctx); err == nil {
		want := map[string]string{}
		for _, c := range lv.Components {
			want[c.ID] = c.Want
		}
		for _, dep := range components.Default().Dependents(cid) {
			if !named[dep] && want[dep] == components.WantEnabled {
				out.Also = append(out.Also, l.alsoOf(dep, components.PhaseWaiting))
			}
		}
	}
	out.QueuedJobsParked, out.DrainS = p.QueuedJobsParked, p.DrainS
	return out, nil
}

// publishedFlow is a Maktaba-published plugin's flow: its stages, the stages they hand
// work to and the stages that hand work to them (the manifests' edges), each stage marked
// with the plugin it belongs to.
func (l *Lifecycle) publishedFlow(ctx context.Context, fp *firstparty.FirstParty) (*FlowView, error) {
	states := map[string]string{}
	if l.Components != nil {
		lv, err := l.Components.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range lv.Components {
			states[c.ID] = pluginflow.StateInstalled
			if c.Want == components.WantEnabled {
				states[c.ID] = pluginflow.StateEnabled
			}
		}
	}
	type edge struct{ from, to string }
	owner := map[string]*firstparty.FirstParty{}
	subject := map[string]string{}
	var edges []edge
	list, _ := firstparty.Plugins()
	for _, o := range list {
		stages, _ := o.Manifest["stage"].([]any)
		for _, s := range stages {
			st, _ := s.(map[string]any)
			name, _ := st["name"].(string)
			owner[name] = o
			subject[name], _ = st["subject"].(string)
			es, _ := st["edges"].([]any)
			for _, e := range es {
				em, _ := e.(map[string]any)
				if to, _ := em["to"].(string); to != "" {
					edges = append(edges, edge{name, to})
				}
			}
		}
	}
	keep := map[string]bool{}
	for _, s := range fp.Stages {
		keep[s] = true
	}
	// a stage of Maktaba itself is drawn as core, never as a plugin's
	nodeID := func(s string) string {
		if o := owner[s]; IsPublished(o.ID) {
			return "stage:" + o.ID + "/" + s
		}
		return "core:" + s
	}
	g := pluginflow.Graph{Edges: []pluginflow.Edge{}, Nodes: []pluginflow.Node{}}
	seen := map[string]bool{}
	for _, e := range edges {
		if (!keep[e.from] && !keep[e.to]) || owner[e.to] == nil {
			continue
		}
		seen[e.from], seen[e.to] = true, true
		g.Edges = append(g.Edges, pluginflow.Edge{From: nodeID(e.from), Kind: "after", To: nodeID(e.to)})
	}
	for s := range keep {
		seen[s] = true
	}
	for s := range seen {
		o := owner[s]
		if !IsPublished(o.ID) {
			g.Nodes = append(g.Nodes, pluginflow.Node{ID: nodeID(s), Kind: "core", Name: s, State: pluginflow.StateEnabled, Subject: subject[s]})
			continue
		}
		state := states[o.Trusted.Component]
		if state == "" {
			state = pluginflow.StateEnabled
		}
		g.Nodes = append(g.Nodes, pluginflow.Node{ID: nodeID(s), Kind: "stage", Name: s, Plugin: o.ID, State: state, Subject: subject[s]})
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
	return &FlowView{ID: fp.ID, Version: fp.Version, Graph: g, Missing: []FlowMember{}, Findings: []FlowFinding{}}, nil
}

// withRelease refuses what a plugin that comes with the release cannot do yet.
func withRelease(id string) *Problem {
	return problem(http.StatusConflict, CodeWithRelease, "%s comes with Maktaba: it is updated with Maktaba and cannot be removed or changed on its own yet", id)
}

// RefuseWithRelease answers the refusal for id when Maktaba publishes it and it came with
// the release (nil for any other plugin).
func RefuseWithRelease(id string) error {
	if IsPublished(id) {
		return withRelease(id)
	}
	return nil
}
