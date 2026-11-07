// Package pluginflow is the flow check of Epic 54 (README §4.12, story 54.39): the combined
// graph of core events, plugin stages and published events, the leaf rule, existence and
// subjects, cycles (Tarjan's strongly connected components) and depth. The TypeScript
// checker (tools/plugin-sdk/js/src/flow.ts) is the same algorithm; both answer every case
// of shared/plugins/testdata/flows with the same codes, paths and normalised graph.
//
// The home runs Check over every enabled plugin plus a change at install, enable and
// update; the store runs it at verification (54.16 C12) with Strict, where a missing
// upstream is a refusal instead of a warning.
package pluginflow

import (
	"bytes"
	"container/heap"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	plugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

// Codes and severities.
const (
	CodeInvalid          = "flow_invalid"
	CodeLimit            = "flow_limit"
	CodeDuplicate        = "flow_duplicate"
	CodeNotMember        = "flow_not_member"
	CodeManifestMismatch = "flow_manifest_mismatch"
	CodeCoreEdge         = "flow_core_edge"
	CodeForeignStage     = "flow_foreign_stage"
	CodeForeignEvent     = "flow_foreign_event"
	CodeUnknownEvent     = "flow_unknown_event"
	CodeSubjectMismatch  = "flow_subject_mismatch"
	CodeMissingPlugin    = "flow_missing_plugin"
	CodeCycle            = "flow_cycle"
	CodeTooDeep          = "flow_too_deep"

	Refuse = "refuse"
	Warn   = "warn"

	// MaxHops is the lineage hop cap (README §4.7): the longest declared chain of stages.
	MaxHops = 16
	// MaxStages is the largest combined graph a home checks.
	MaxStages = 2000

	StateEnabled   = "enabled"
	StateInstalled = "installed"
	StateMissing   = "missing"
)

// Stage is one stage's place in the graph.
type Stage struct {
	Name       string
	Subject    string
	Triggers   []string // "audio.ready", "stage:<own>", "plugin:<id>/<event>"
	Emits      []string
	MayRequest []string
}

// Event is one published event.
type Event struct {
	Name    string
	Subject string
	Payload string
}

// Member is one plugin as the graph sees it: from its manifest (Version exact, Depends
// with ranges) or from a flow (Version a range, Depends nil).
type Member struct {
	ID      string
	Version string
	Stages  []Stage
	Events  []Event
	Depends map[string]string
	State   string // "" or enabled: in the graph; installed or missing: shown only
}

func (m Member) active() bool { return m.State == "" || m.State == StateEnabled }

// Finding is one refusal or warning.
type Finding struct {
	Code     string   `json:"code"`
	Severity string   `json:"severity"`
	Plugin   string   `json:"plugin,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	Path     []string `json:"path,omitempty"`
}

// Node and Edge make the normalised graph the chart draws (54.41).
type Node struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Plugin  string `json:"plugin"`
	State   string `json:"state"`
	Subject string `json:"subject"`
}

type Edge struct {
	From string `json:"from"`
	Kind string `json:"kind"`
	To   string `json:"to"`
}

type Graph struct {
	Edges []Edge `json:"edges"`
	Nodes []Node `json:"nodes"`
}

// JSON is the graph's one text: sorted, no spaces, unescaped.
func (g Graph) JSON() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(g)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// Options: Strict refuses a missing upstream (the store); the home only warns.
type Options struct {
	Strict bool
}

// HasRefusal reports whether any finding refuses.
func HasRefusal(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == Refuse {
			return true
		}
	}
	return false
}

// FromManifest reads a normalised manifest (pluginmanifest.Validate) as a member.
func FromManifest(m map[string]any, state string) Member {
	out := Member{ID: str(m["id"]), Version: str(m["version"]), State: state, Depends: map[string]string{}}
	for _, s := range list(m["stage"]) {
		st := s.(map[string]any)
		g := Stage{Name: str(st["name"]), Subject: str(st["subject"]), Emits: strs(st["emits"]), MayRequest: strs(st["may_request"])}
		for _, t := range list(st["trigger"]) {
			tm := t.(map[string]any)
			if e, ok := tm["event"].(string); ok {
				g.Triggers = append(g.Triggers, e)
			} else {
				g.Triggers = append(g.Triggers, "stage:"+str(tm["stage"]))
			}
		}
		out.Stages = append(out.Stages, g)
	}
	for _, e := range list(m["event"]) {
		em := e.(map[string]any)
		out.Events = append(out.Events, Event{Name: str(em["name"]), Subject: str(em["subject"]), Payload: str(em["payload"])})
	}
	for _, d := range list(m["depends"]) {
		dm := d.(map[string]any)
		out.Depends[str(dm["plugin"])] = str(dm["version"])
	}
	return out
}

// Flow is a parsed flow.toml.
type Flow struct {
	ID      string
	Version string
	Name    string
	Members []Member
}

// ParseFlow reads and checks a flow.toml's schema.
func ParseFlow(src []byte) (Flow, []Finding) {
	doc, errs := pluginmanifest.ValidateFlowDoc(src)
	if errs != nil {
		out := make([]Finding, 0, len(errs))
		for _, e := range errs {
			out = append(out, Finding{Code: e.Code, Severity: Refuse, Detail: e.Detail})
		}
		return Flow{}, out
	}
	f := Flow{ID: str(doc["id"]), Version: str(doc["version"]), Name: str(doc["strings"].(map[string]any)["name"])}
	for _, p := range list(doc["plugin"]) {
		pm := p.(map[string]any)
		m := Member{ID: str(pm["id"]), Version: normaliseRange(str(pm["version"])), State: StateEnabled}
		for _, s := range list(pm["stage"]) {
			st := s.(map[string]any)
			m.Stages = append(m.Stages, Stage{Name: str(st["name"]), Subject: str(st["subject"]),
				Triggers: strs(st["triggers"]), Emits: strs(st["emits"]), MayRequest: strs(st["may_request"])})
		}
		for _, e := range list(pm["event"]) {
			em := e.(map[string]any)
			m.Events = append(m.Events, Event{Name: str(em["name"]), Subject: str(em["subject"]), Payload: str(em["payload"])})
		}
		f.Members = append(f.Members, m)
	}
	return f, nil
}

// MatchManifest checks that the package's manifest is a member of its flow and matches
// its entry exactly (54.39 §4, checks 2 and 3).
func MatchManifest(f Flow, manifest map[string]any) []Finding {
	m := FromManifest(manifest, "")
	var entry *Member
	for i := range f.Members {
		if f.Members[i].ID == m.ID {
			entry = &f.Members[i]
		}
	}
	if entry == nil || !InRange(m.Version, entry.Version) {
		return []Finding{{Code: CodeNotMember, Severity: Refuse, Plugin: m.ID}}
	}
	if field := mismatch(m, *entry); field != "" {
		return []Finding{{Code: CodeManifestMismatch, Severity: Refuse, Plugin: m.ID, Detail: field}}
	}
	return nil
}

func mismatch(m, e Member) string {
	byName := func(ss []Stage) map[string]Stage {
		out := map[string]Stage{}
		for _, s := range ss {
			out[s.Name] = s
		}
		return out
	}
	ms, es := byName(m.Stages), byName(e.Stages)
	if !sameSet(keys(ms), keys(es)) {
		return "stages"
	}
	for _, name := range sortedKeys(ms) {
		a, b := ms[name], es[name]
		switch {
		case a.Subject != b.Subject:
			return "stage " + name + " subject"
		case !sameSet(a.Triggers, b.Triggers):
			return "stage " + name + " triggers"
		case !sameSet(a.Emits, b.Emits):
			return "stage " + name + " emits"
		case !sameSet(a.MayRequest, b.MayRequest):
			return "stage " + name + " may_request"
		}
	}
	ev := func(es []Event) []string {
		out := make([]string, 0, len(es))
		for _, e := range es {
			out = append(out, e.Name+"|"+e.Subject+"|"+e.Payload)
		}
		return out
	}
	if !sameSet(ev(m.Events), ev(e.Events)) {
		return "events"
	}
	if !sameSet(sortedKeys(m.Depends), publishers(e)) {
		return "depends"
	}
	return ""
}

func publishers(m Member) []string {
	seen := map[string]bool{}
	for _, s := range m.Stages {
		for _, t := range s.Triggers {
			if p, _, ok := subscription(t); ok {
				seen[p] = true
			}
		}
	}
	return sortedKeys(seen)
}

// subscription splits "plugin:<id>/<event>".
func subscription(t string) (publisher, event string, ok bool) {
	rest, ok := strings.CutPrefix(t, "plugin:")
	if !ok {
		return "", "", false
	}
	publisher, event, ok = strings.Cut(rest, "/")
	return publisher, event, ok
}

type builder struct {
	nodes    map[string]Node
	edges    map[Edge]bool
	findings []Finding
}

func (b *builder) node(n Node) {
	if _, ok := b.nodes[n.ID]; !ok {
		b.nodes[n.ID] = n
	}
}

func (b *builder) find(f Finding) { b.findings = append(b.findings, f) }

func coreStages() map[string]bool {
	out := map[string]bool{}
	for _, e := range pluginmanifest.Events() {
		if e.Stage != "" {
			out[e.Stage] = true
		}
	}
	return out
}

func stageID(p, s string) string { return "stage:" + p + "/" + s }
func eventID(p, e string) string { return "event:" + p + "/" + e }

// Check builds the combined graph of the active members and checks it (54.39 §4,
// checks 4 to 8). The members not active (installed but off, or missing) are left out of
// the graph; a subscription to one is a missing upstream.
func Check(members []Member, opts Options) (Graph, []Finding) {
	n := 0
	for _, m := range members {
		n += len(m.Stages) + len(m.Events)
	}
	b := &builder{nodes: make(map[string]Node, n), edges: make(map[Edge]bool, 2*n)}
	sorted := append([]Member(nil), members...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	byID := map[string]Member{}
	var active []Member
	stages := 0
	for _, m := range sorted {
		prev, seen := byID[m.ID]
		switch {
		case m.active() && seen && prev.active():
			b.find(Finding{Code: CodeDuplicate, Severity: Refuse, Plugin: m.ID, Detail: m.ID})
		case m.active():
			byID[m.ID] = m
			active = append(active, m)
		case !seen:
			byID[m.ID] = m
		}
	}
	core := coreStages()
	missingSeverity := Warn
	if opts.Strict {
		missingSeverity = Refuse
	}
	for _, m := range active {
		own := make(map[string]Stage, len(m.Stages))
		for _, s := range m.Stages {
			own[s.Name] = s
		}
		pub := map[string]Event{}
		for _, e := range m.Events {
			pub[e.Name] = e
			b.node(Node{ID: eventID(m.ID, e.Name), Kind: "event", Name: e.Name, Plugin: m.ID, State: StateEnabled, Subject: e.Subject})
		}
		for _, s := range m.Stages {
			stages++
			to := stageID(m.ID, s.Name)
			b.node(Node{ID: to, Kind: "stage", Name: s.Name, Plugin: m.ID, State: StateEnabled, Subject: s.Subject})
			where := m.ID + "/" + s.Name
			for _, t := range s.Triggers {
				if name, ok := strings.CutPrefix(t, "stage:"); ok {
					from, mine := own[name]
					switch {
					case mine && from.Subject != s.Subject:
						b.find(Finding{Code: CodeSubjectMismatch, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + t})
					case mine:
						b.edges[Edge{From: stageID(m.ID, name), Kind: "after", To: to}] = true
					case core[name]:
						b.find(Finding{Code: CodeCoreEdge, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + t})
					default:
						b.find(Finding{Code: CodeForeignStage, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + t})
					}
					continue
				}
				if publisher, ev, ok := subscription(t); ok {
					up, found := byID[publisher]
					rng := m.Depends[publisher]
					switch {
					case publisher == m.ID:
						b.find(Finding{Code: CodeInvalid, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + t})
					case !found || !up.active() || (rng != "" && !isRange(up.Version) && !InRange(up.Version, rng)):
						detail := publisher
						if rng != "" {
							detail += " " + rng
						}
						b.find(Finding{Code: CodeMissingPlugin, Severity: missingSeverity, Plugin: m.ID, Detail: detail})
					default:
						e, has := eventOf(up, ev)
						switch {
						case !has:
							b.find(Finding{Code: CodeUnknownEvent, Severity: Refuse, Plugin: m.ID, Detail: t})
						case e.Subject != s.Subject:
							b.find(Finding{Code: CodeSubjectMismatch, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + t})
						default:
							b.edges[Edge{From: eventID(publisher, ev), Kind: "subscribe", To: to}] = true
						}
					}
					continue
				}
				e, ok := pluginmanifest.LookupEvent(t)
				switch {
				case ok && e.Triggerable() && e.Subject != s.Subject:
					b.find(Finding{Code: CodeSubjectMismatch, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + t})
				case ok && e.Triggerable():
					b.node(Node{ID: "core:" + t, Kind: "core", Name: t, State: StateEnabled, Subject: e.Subject})
					b.edges[Edge{From: "core:" + t, Kind: "trigger", To: to}] = true
				case core[t]:
					b.find(Finding{Code: CodeCoreEdge, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + t})
				default:
					b.find(Finding{Code: CodeUnknownEvent, Severity: Refuse, Plugin: m.ID, Detail: t})
				}
			}
			for _, r := range s.MayRequest {
				switch {
				case own[r].Name != "":
					b.edges[Edge{From: to, Kind: "request", To: stageID(m.ID, r)}] = true
				case core[r]:
					b.find(Finding{Code: CodeCoreEdge, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + r})
				default:
					b.find(Finding{Code: CodeForeignStage, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + r})
				}
			}
			for _, e := range s.Emits {
				if _, ok := pub[e]; ok {
					b.edges[Edge{From: to, Kind: "emit", To: eventID(m.ID, e)}] = true
				} else {
					b.find(Finding{Code: CodeForeignEvent, Severity: Refuse, Plugin: m.ID, Detail: where + ": " + e})
				}
			}
		}
	}
	if stages > MaxStages {
		b.find(Finding{Code: CodeLimit, Severity: Refuse, Detail: "stages " + strconv.Itoa(stages)})
	}
	g := b.graph()
	adj := adjacency(g)
	cycles := cyclicComponents(g, adj)
	for _, comp := range cycles {
		b.find(Finding{Code: CodeCycle, Severity: Refuse, Plugin: pluginOf(comp[0]), Path: display(shortestCycle(comp, adj))})
	}
	if len(cycles) == 0 {
		if path := longest(g, adj); stageCount(path) > MaxHops {
			b.find(Finding{Code: CodeTooDeep, Severity: Refuse, Plugin: pluginOf(path[len(path)-1]), Path: display(path)})
		}
	}
	return g, b.findings
}

func eventOf(m Member, name string) (Event, bool) {
	for _, e := range m.Events {
		if e.Name == name {
			return e, true
		}
	}
	return Event{}, false
}

func (b *builder) graph() Graph {
	g := Graph{Nodes: make([]Node, 0, len(b.nodes)), Edges: make([]Edge, 0, len(b.edges))}
	for _, n := range b.nodes {
		g.Nodes = append(g.Nodes, n)
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	for e := range b.edges {
		g.Edges = append(g.Edges, e)
	}
	sort.Slice(g.Edges, func(i, j int) bool {
		a, c := g.Edges[i], g.Edges[j]
		if a.From != c.From {
			return a.From < c.From
		}
		if a.To != c.To {
			return a.To < c.To
		}
		return a.Kind < c.Kind
	})
	return g
}

// adjacency: each node's successors, sorted, without duplicates.
func adjacency(g Graph) map[string][]string {
	adj := map[string][]string{}
	for _, e := range g.Edges {
		s := adj[e.From]
		if len(s) == 0 || s[len(s)-1] != e.To {
			adj[e.From] = append(s, e.To)
		}
	}
	return adj
}

// cyclicComponents: Tarjan's strongly connected components that hold a cycle (two or
// more nodes, or one with an edge to itself), each with its smallest stage first, ordered
// by that stage.
func cyclicComponents(g Graph, adj map[string][]string) [][]string {
	index, low := make(map[string]int, len(g.Nodes)), make(map[string]int, len(g.Nodes))
	onStack := make(map[string]bool, len(g.Nodes))
	var stack []string
	var out [][]string
	next := 0
	var strong func(v string)
	strong = func(v string) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if _, seen := index[w]; !seen {
				strong(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			self := false
			for _, w := range adj[v] {
				if w == v {
					self = true
				}
			}
			if len(comp) > 1 || self {
				out = append(out, stageFirst(comp))
			}
		}
	}
	for _, n := range g.Nodes {
		if _, seen := index[n.ID]; !seen {
			strong(n.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// stageFirst sorts a component and puts its smallest stage first: a cycle always passes
// through a stage, and a path that starts at one reads as "this stage starts…".
func stageFirst(comp []string) []string {
	sort.Strings(comp)
	for i, c := range comp {
		if strings.HasPrefix(c, "stage:") {
			return append([]string{c}, append(append([]string(nil), comp[:i]...), comp[i+1:]...)...)
		}
	}
	return comp
}

// shortestCycle: from the component's smallest stage, the shortest way back to it inside
// the component, by breadth-first search with successors in sorted order.
func shortestCycle(comp []string, adj map[string][]string) []string {
	in := map[string]bool{}
	for _, c := range comp {
		in[c] = true
	}
	start := comp[0]
	parent := map[string]string{}
	queue := []string{start}
	seen := map[string]bool{start: true}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, v := range adj[u] {
			if !in[v] {
				continue
			}
			if v == start {
				path := []string{start}
				for x := u; x != start; x = parent[x] {
					path = append(path, x)
				}
				for i, j := 1, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				return append(path, start)
			}
			if !seen[v] {
				seen[v] = true
				parent[v] = u
				queue = append(queue, v)
			}
		}
	}
	return []string{start, start}
}

// longest: the path with the most stages, in topological order taking the smallest
// ready node first; ties go to the smallest predecessor and the smallest end node.
func longest(g Graph, adj map[string][]string) []string {
	indeg := make(map[string]int, len(g.Nodes))
	preds := make(map[string][]string, len(g.Nodes))
	for _, n := range g.Nodes {
		indeg[n.ID] += 0
	}
	for from, tos := range adj {
		for _, to := range tos {
			indeg[to]++
			preds[to] = append(preds[to], from)
		}
	}
	ready := &idHeap{}
	for _, n := range g.Nodes {
		if indeg[n.ID] == 0 {
			heap.Push(ready, n.ID)
		}
	}
	best := make(map[string]int, len(g.Nodes))
	pred := make(map[string]string, len(g.Nodes))
	end := ""
	for ready.Len() > 0 {
		v := heap.Pop(ready).(string)
		ps := preds[v]
		sort.Strings(ps)
		b, p := 0, ""
		for _, u := range ps {
			if best[u] > b || p == "" {
				b, p = best[u], u
			}
		}
		if strings.HasPrefix(v, "stage:") {
			b++
		}
		best[v], pred[v] = b, p
		if end == "" || b > best[end] || (b == best[end] && v < end) {
			end = v
		}
		for _, w := range adj[v] {
			indeg[w]--
			if indeg[w] == 0 {
				heap.Push(ready, w)
			}
		}
	}
	if end == "" {
		return nil
	}
	var path []string
	for x := end; x != ""; x = pred[x] {
		path = append(path, x)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// idHeap hands out the smallest ready node first.
type idHeap []string

func (h idHeap) Len() int           { return len(h) }
func (h idHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h idHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *idHeap) Push(x any)        { *h = append(*h, x.(string)) }
func (h *idHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

func stageCount(path []string) int {
	n := 0
	for _, p := range path {
		if strings.HasPrefix(p, "stage:") {
			n++
		}
	}
	return n
}

// display writes node ids as people read them: audio.ready, <plugin>/<stage>,
// plugin:<plugin>/<event>.
func display(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		kind, rest, _ := strings.Cut(id, ":")
		switch kind {
		case "core", "stage":
			out[i] = rest
		default:
			out[i] = "plugin:" + rest
		}
	}
	return out
}

func pluginOf(id string) string {
	_, rest, _ := strings.Cut(id, ":")
	p, _, _ := strings.Cut(rest, "/")
	if strings.HasPrefix(id, "core:") {
		return ""
	}
	return p
}

// Message is a finding in English ("en") or Arabic ("ar"), from flow-messages.json.
func Message(f Finding, lang string) string {
	var all map[string]map[string]string
	_ = json.Unmarshal(plugins.FlowMessages, &all)
	t := all[f.Code][lang]
	if t == "" {
		t = all[f.Code]["en"]
	}
	return strings.NewReplacer("{plugin}", f.Plugin, "{detail}", f.Detail, "{path}", strings.Join(f.Path, " → ")).Replace(t)
}

// ---- semver ranges ----------------------------------------------------------------

func isRange(v string) bool { return strings.ContainsAny(v, "<>=") }

func normaliseRange(r string) string {
	parts := strings.Split(r, ",")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(p, " ", "")
	}
	return strings.Join(parts, ", ")
}

// InRange reports whether version meets every comparator of rng (">=1.2.0, <2.0.0").
// A pre-release sorts before its release; two pre-releases compare as text.
func InRange(version, rng string) bool {
	for _, c := range strings.Split(normaliseRange(rng), ", ") {
		op := ""
		for _, o := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(c, o) {
				op = o
				break
			}
		}
		want := strings.TrimPrefix(c, op)
		d := compare(version, want)
		ok := map[string]bool{">=": d >= 0, ">": d > 0, "<=": d <= 0, "<": d < 0, "=": d == 0}[op]
		if !ok {
			return false
		}
	}
	return true
}

func compare(a, b string) int {
	ac, apre, _ := strings.Cut(a, "-")
	bc, bpre, _ := strings.Cut(b, "-")
	as, bs := strings.Split(ac, "."), strings.Split(bc, ".")
	for i := 0; i < 3; i++ {
		x, _ := strconv.Atoi(at(as, i))
		y, _ := strconv.Atoi(at(bs, i))
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	case apre < bpre:
		return -1
	}
	return 1
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "0"
}

// ---- helpers ----------------------------------------------------------------------

func str(v any) string { s, _ := v.(string); return s }

func list(v any) []any { l, _ := v.([]any); return l }

func strs(v any) []string {
	var out []string
	for _, x := range list(v) {
		out = append(out, str(x))
	}
	return out
}

func keys[V any](m map[string]V) []string { return sortedKeys(m) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// HomeGraph is the whole home's plugin graph for the chart (54.41): every member, the off
// and missing ones too, each node marked with its plugin's state; and the findings of the
// active members alone, as Check gives them.
func HomeGraph(members []Member) (Graph, []Finding) {
	_, findings := Check(members, Options{})
	all := make([]Member, len(members))
	state := map[string]string{}
	for i, m := range members {
		all[i] = m
		all[i].State = StateEnabled
		s := m.State
		if s == "" {
			s = StateEnabled
		}
		state[m.ID] = s
	}
	g, _ := Check(all, Options{})
	for i, n := range g.Nodes {
		if n.Plugin != "" {
			g.Nodes[i].State = state[n.Plugin]
		}
	}
	return g, findings
}

// Downstream answers the stages of other plugins that run, directly or further down, on
// the events of plugin id: what goes idle when it is off (54.39 §6).
func Downstream(g Graph, id string) []string {
	adj := adjacency(g)
	var queue []string
	seen := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Plugin == id && n.Kind == "event" {
			queue = append(queue, n.ID)
			seen[n.ID] = true
		}
	}
	var out []string
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for _, v := range adj[u] {
			if seen[v] {
				continue
			}
			seen[v] = true
			queue = append(queue, v)
			if strings.HasPrefix(v, "stage:") && pluginOf(v) != id {
				out = append(out, display([]string{v})[0])
			}
		}
	}
	sort.Strings(out)
	return out
}

// Lookup answers the plugin a store or a home holds under id, at the newest version a
// range accepts ("" any): false when it holds none.
type Lookup func(id, rng string) (Member, bool)

// ResolveDepends adds to members every plugin they subscribe to that they do not hold,
// from lookup, and then the plugins those subscribe to, and so on: a cycle or a missing
// upstream two or more plugins away is then in the graph Check walks. A publisher lookup
// does not hold stays out (Check reports it missing). It stops at MaxStages stages.
func ResolveDepends(members []Member, lookup Lookup) []Member {
	out := append([]Member(nil), members...)
	have := map[string]bool{}
	stages := 0
	for _, m := range out {
		have[m.ID] = true
		stages += len(m.Stages)
	}
	asked := map[string]bool{}
	for i := 0; i < len(out); i++ {
		m := out[i]
		var want []string
		for _, s := range m.Stages {
			for _, t := range s.Triggers {
				if p, _, ok := subscription(t); ok && !have[p] && !asked[p] {
					want = append(want, p)
					asked[p] = true
				}
			}
		}
		sort.Strings(want)
		for _, p := range want {
			if stages > MaxStages {
				return out
			}
			if up, ok := lookup(p, m.Depends[p]); ok {
				up.State = ""
				out = append(out, up)
				have[p] = true
				stages += len(up.Stages)
			}
		}
	}
	return out
}
