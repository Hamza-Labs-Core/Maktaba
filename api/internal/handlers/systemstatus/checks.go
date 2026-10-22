package systemstatus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/processes"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

func base(id, group, kind, name string) Item {
	return Item{ID: id, Group: group, Kind: kind, Name: name, Help: kind}
}

// checks is the round, in page order inside each group.
func (h *Handler) checks() []check {
	cs := []check{
		{Name: "api", Fallback: crit(base("api", GroupServices, KindAPI, "API")), Run: checkAPI},
		{Name: "postgres", Fallback: crit(base("postgres", GroupServices, KindPostgres, "Database")), Run: checkPostgres},
		{Name: "streaming", Fallback: base("streaming", GroupServices, KindStreaming, "Streaming"), Run: checkStreaming},
		{Name: "pipeline", Fallback: base("pipeline", GroupServices, KindPipeline, "Pipeline"), Run: checkPipeline},
	}
	if h.SupervisorURL != "" {
		cs = append(cs, check{Name: "processes",
			Fallback: base("processes", GroupServices, KindProcess, "Processes"), Run: checkProcesses})
	}
	return append(cs,
		check{Name: "workers", Fallback: crit(base("workers", GroupWorkers, KindWorkers, "Workers")), Run: checkWorkers},
		check{Name: "streaming_hosts", Fallback: base("streaming_hosts", GroupWorkers, KindStreamingHost, "Streaming machines"),
			Run: checkStreamingHosts},
		check{Name: "dependencies", Fallback: base("dependencies", GroupDependencies, KindDependency, "Dependencies"),
			Run: checkDependencies},
		check{Name: "plex", Fallback: base("plex_livetv", GroupIntegrations, KindPlexLiveTV, "Plex Live TV"), Run: checkPlex},
		check{Name: "queues", Fallback: base("queues", GroupQueuesTV, KindQueues, "Queues"), Run: checkQueues},
		check{Name: "tv_channels", Fallback: base("tv_channels", GroupQueuesTV, KindTVChannels, "Channels on air"),
			Run: checkTVChannels},
		check{Name: "tv_change", Fallback: base("tv_change", GroupQueuesTV, KindTVChange, "TV change"), Run: checkTVChange},
		check{Name: "tv_slot", Fallback: base("tv_slot", GroupQueuesTV, KindTVSlot, "Live TV slot"), Run: checkTVSlots},
		check{Name: "tv_tuner", Fallback: base("tv_tuner", GroupQueuesTV, KindTVTuner, "TV tuner"), Run: checkTVTuner},
	)
}

func crit(it Item) Item { it.Critical = true; return it }

func one(it Item) []Item { return []Item{it} }

func errText(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// ---------- services ----------

func checkAPI(_ context.Context, e *env) []Item {
	it := crit(base("api", GroupServices, KindAPI, "API"))
	v := e.h.Version
	if v == "" {
		v = "dev"
	}
	up := time.Duration(0)
	if !e.h.StartTime.IsZero() {
		up = e.now.Sub(e.h.StartTime).Truncate(time.Second)
	}
	it.set(StatusOK, "api_ok", map[string]any{"version": v, "uptime_seconds": int64(up.Seconds())},
		fmt.Sprintf("Version %s, up %s.", v, up))
	it.Details = map[string]any{"version": v, "uptime_seconds": int64(up.Seconds())}
	return one(it)
}

func checkPostgres(ctx context.Context, e *env) []Item {
	it := crit(base("postgres", GroupServices, KindPostgres, "Database"))
	db := e.h.DB
	if db == nil {
		it.set(StatusDown, "postgres_not_configured", nil, "No database is configured (DATABASE_URL).")
		return one(it)
	}
	start := time.Now()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil {
		it.set(StatusDown, "postgres_unreachable", map[string]any{"detail": errText(err)},
			"The database does not answer: "+errText(err))
		return one(it)
	}
	ms := time.Since(start).Milliseconds()
	it.Details = map[string]any{"latency_ms": ms}
	// replication, where Postgres can tell: this server a replica, and the
	// replicas streaming from it (best effort: a role may not see them)
	var inRecovery bool
	if err := db.QueryRowContext(ctx, `SELECT pg_is_in_recovery()`).Scan(&inRecovery); err == nil {
		if inRecovery {
			it.Details["role"] = "replica"
		} else {
			it.Details["role"] = "primary"
		}
	}
	var replicas int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_replication`).Scan(&replicas); err == nil {
		it.Details["replicas"] = replicas
	}
	params := map[string]any{"ms": ms, "limit": e.rules.DBLatencyMs}
	if ms > int64(e.rules.DBLatencyMs) {
		it.set(StatusDegraded, "postgres_slow", params,
			fmt.Sprintf("Slow: a query took %d ms (slow from %d ms).", ms, e.rules.DBLatencyMs))
		return []Item{it}
	}
	it.set(StatusOK, "postgres_ok", params, fmt.Sprintf("Answers in %d ms.", ms))
	return []Item{it}
}

func (h *Handler) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (h *Handler) dial(ctx context.Context, addr string) error {
	d := h.Dial
	if d == nil {
		d = (&net.Dialer{}).DialContext
	}
	c, err := d(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// httpOK GETs url and wants a 200.
func (h *Handler) httpOK(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %d", resp.StatusCode)
	}
	return nil
}

func checkStreaming(ctx context.Context, e *env) []Item {
	it := base("streaming", GroupServices, KindStreaming, "Streaming")
	onAir, stale, encoders, online, hosts := 0, 0, map[string]int{}, 0, 0
	if cl, err := e.tvCluster(ctx); err == nil {
		for _, c := range cl.OnAir {
			if c.Stale {
				stale++
				continue
			}
			onAir++
			if c.Encoder != "" {
				encoders[c.Encoder]++
			}
		}
		hosts = len(cl.Hosts)
		for _, x := range cl.Hosts {
			if x.State == "online" {
				online++
			}
		}
	}
	// streaming down while channels are live takes the TV off the air
	it.Critical = onAir > 0
	sessions := 0
	for _, n := range encoders {
		sessions += n
	}
	it.Details = map[string]any{"url": e.h.StreamingURL, "grpc_addr": e.h.StreamingAddr,
		"on_air": onAir, "encoder_sessions": sessions, "encoders": encoders,
		"hosts_online": online, "hosts": hosts}
	if e.h.StreamingURL == "" {
		it.set(StatusDegraded, "streaming_not_configured", nil,
			"The streaming service's address is not set (MAKTABA_STREAMING_HTTP_URL).")
		return one(it)
	}
	if err := e.h.httpOK(ctx, strings.TrimRight(e.h.StreamingURL, "/")+"/healthz"); err != nil {
		it.set(StatusDown, "streaming_unreachable",
			map[string]any{"url": e.h.StreamingURL, "detail": errText(err), "on_air": onAir},
			fmt.Sprintf("Does not answer at %s: %s", e.h.StreamingURL, errText(err)))
		return one(it)
	}
	if e.h.StreamingAddr != "" {
		if err := e.h.dial(ctx, e.h.StreamingAddr); err != nil {
			it.set(StatusDegraded, "streaming_grpc_unreachable",
				map[string]any{"addr": e.h.StreamingAddr, "detail": errText(err)},
				fmt.Sprintf("Its control port %s does not answer: %s", e.h.StreamingAddr, errText(err)))
			return one(it)
		}
	}
	it.set(StatusOK, "streaming_ok", map[string]any{"on_air": onAir, "sessions": sessions},
		fmt.Sprintf("Answers; %d channels on air, %d encoder sessions.", onAir, sessions))
	return one(it)
}

func checkPipeline(ctx context.Context, e *env) []Item {
	it := base("pipeline", GroupServices, KindPipeline, "Pipeline")
	addr := e.h.PipelineAddr
	it.Details = map[string]any{"addr": addr}
	if addr == "" {
		it.set(StatusDegraded, "pipeline_not_configured", nil,
			"Not configured (MAKTABA_PIPELINE_ADDR): search finds words only, not meaning.")
		return one(it)
	}
	if err := e.h.dial(ctx, addr); err != nil {
		it.set(StatusDown, "pipeline_unreachable", map[string]any{"addr": addr, "detail": errText(err)},
			fmt.Sprintf("Does not answer at %s: %s", addr, errText(err)))
		return one(it)
	}
	it.set(StatusOK, "pipeline_ok", map[string]any{"addr": addr}, "Answers at "+addr+".")
	return one(it)
}

// processState maps the supervisor's process states.
func processState(state string, crashLoop bool) (string, string) {
	switch {
	case crashLoop:
		return StatusDown, "process_crash_loop"
	case state == "running":
		return StatusOK, "process_running"
	case state == "failed":
		return StatusDown, "process_failed"
	case state == "starting":
		return StatusDegraded, "process_starting"
	case state == "unhealthy":
		return StatusDegraded, "process_unhealthy"
	case state == "backoff":
		return StatusDegraded, "process_restarting"
	}
	return StatusDegraded, "process_stopped"
}

var processReasons = map[string]string{
	"process_crash_loop": "Keeps exiting soon after it starts.",
	"process_running":    "Running.",
	"process_failed":     "Could not be kept running.",
	"process_starting":   "Starting.",
	"process_unhealthy":  "Running but not responding.",
	"process_restarting": "Stopped; restarting soon.",
	"process_stopped":    "Not running.",
}

func checkProcesses(ctx context.Context, e *env) []Item {
	var doc struct {
		Processes []struct {
			Role      string `json:"role"`
			State     string `json:"state"`
			Restarts  int    `json:"restarts"`
			CrashLoop bool   `json:"crash_loop"`
			UptimeSec int64  `json:"uptime_sec"`
		} `json:"processes"`
	}
	raw, err := processes.Fetch(ctx, e.h.client(), e.h.SupervisorURL)
	if err == nil {
		b, _ := json.Marshal(raw)
		err = json.Unmarshal(b, &doc)
	}
	if err != nil {
		it := base("processes", GroupServices, KindProcess, "Processes")
		it.set(StatusDegraded, "process_supervisor_unreachable", map[string]any{"detail": errText(err)},
			"The process supervisor does not answer: "+errText(err))
		return one(it)
	}
	out := []Item{}
	for _, p := range doc.Processes {
		it := base("process:"+p.Role, GroupServices, KindProcess, p.Role)
		st, code := processState(p.State, p.CrashLoop)
		it.set(st, code, map[string]any{"role": p.Role, "restarts": p.Restarts}, processReasons[code])
		it.Details = map[string]any{"state": p.State, "restarts": p.Restarts, "uptime_seconds": p.UptimeSec}
		out = append(out, it)
	}
	return out
}

// ---------- workers ----------

// transcriptionStages: a worker that serves only these is a transcription
// worker (the Mac, a GPU PC); every other is a main worker.
var transcriptionStages = map[string]bool{"transcribe": true, "relisten": true, "subtitle_gen": true}

func workerRole(stages []string) string {
	if len(stages) == 0 {
		return "main"
	}
	for _, s := range stages {
		if !transcriptionStages[s] && !strings.HasPrefix(s, "transcri") {
			return "main"
		}
	}
	return "transcription"
}

const runningByWorkerSQL = `
SELECT claimed_by, stage, count(*) FROM processing_jobs
 WHERE state = 'running' AND claimed_by IS NOT NULL GROUP BY claimed_by, stage`

func runningByWorker(ctx context.Context, db *sql.DB) map[string]map[string]int {
	out := map[string]map[string]int{}
	rows, err := db.QueryContext(ctx, runningByWorkerSQL)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var w, st string
		var n int
		if rows.Scan(&w, &st, &n) != nil {
			continue
		}
		if out[w] == nil {
			out[w] = map[string]int{}
		}
		out[w][st] = n
	}
	return out
}

func ageText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// machineKey groups the workers of one machine: its cluster node, else its host.
func machineKey(w queues.Worker) string {
	if w.NodeID != nil && *w.NodeID != "" {
		return "node:" + *w.NodeID
	}
	return "host:" + strings.ToLower(w.Host)
}

// workerItems turns the registry into items: fresh is ok, late degraded,
// gone down. A worker that is not fresh while a fresh one runs on the same
// machine was replaced (a restart under a new id): history, not a failure;
// so is one silent longer than worker_forget_hours.
func workerItems(ws []queues.Worker, running map[string]map[string]int, now time.Time, r Rules) []Item {
	freshOn := map[string]bool{}
	for _, w := range ws {
		if now.Sub(w.LastSeenAt) <= r.late() {
			freshOn[machineKey(w)] = true
		}
	}
	out := []Item{}
	online, late, gone, live := 0, 0, 0, 0
	for _, w := range ws {
		name := w.Host
		if w.NodeName != nil && *w.NodeName != "" {
			name = *w.NodeName
		}
		age := now.Sub(w.LastSeenAt)
		if age < 0 {
			age = 0
		}
		it := base("worker:"+w.ID, GroupWorkers, KindWorker, name)
		it.LastChecked = w.LastSeenAt
		role := workerRole(w.Stages)
		machine := "home"
		if w.NodeID != nil {
			machine = "node"
		}
		run := running[w.ID]
		if run == nil {
			run = map[string]int{}
		}
		it.Details = map[string]any{"worker_id": w.ID, "host": w.Host, "role": role, "machine": machine,
			"stages": w.Stages, "running": run, "last_seen_at": w.LastSeenAt,
			"age_seconds": int64(age.Seconds()), "version": w.Version}
		p := map[string]any{"age_seconds": secs(age), "role": role}
		switch {
		case age <= r.late():
			online++
			live++
			n := 0
			for _, c := range run {
				n += c
			}
			p["jobs"] = n
			it.set(StatusOK, "worker_ok", p, fmt.Sprintf("Seen %s ago; running %d jobs.", ageText(age), n))
		case freshOn[machineKey(w)]:
			it.History = true
			it.set(StatusOK, "worker_replaced", p,
				fmt.Sprintf("Replaced by a newer worker on the same machine; last seen %s ago.", ageText(age)))
		case age > r.forget():
			it.History = true
			it.set(StatusOK, "worker_forgotten", p,
				fmt.Sprintf("Not seen for %s; kept as history.", ageText(age)))
		case age <= r.gone():
			late++
			live++
			it.set(StatusDegraded, "worker_late", p, fmt.Sprintf("Late: last seen %s ago.", ageText(age)))
		default:
			gone++
			live++
			it.set(StatusDown, "worker_gone", p, fmt.Sprintf("Gone: last seen %s ago.", ageText(age)))
		}
		out = append(out, it)
	}
	// the summary first, then the workers that run, then the history
	sort.SliceStable(out, func(i, j int) bool { return !out[i].History && out[j].History })
	sum := crit(base("workers", GroupWorkers, KindWorkers, "Workers"))
	sum.Link = "/queue"
	p := map[string]any{"online": online, "total": live, "late": late, "gone": gone}
	sum.Details = p
	switch {
	case live == 0:
		sum.set(StatusDown, "workers_none", p, "No worker is running: nothing is processed.")
	case online == 0:
		sum.set(StatusDown, "workers_all_gone", p, "Every worker is gone: nothing is processed.")
	case late+gone > 0:
		sum.set(StatusDegraded, "workers_some_missing", p,
			fmt.Sprintf("%d of %d workers online.", online, live))
	default:
		sum.set(StatusOK, "workers_ok", p, fmt.Sprintf("%d of %d workers online.", online, live))
	}
	return append([]Item{sum}, out...)
}

func checkWorkers(ctx context.Context, e *env) []Item {
	if e.h.DB == nil {
		it := crit(base("workers", GroupWorkers, KindWorkers, "Workers"))
		it.set(StatusDown, "workers_unreadable", map[string]any{"detail": "no database"},
			"The worker list cannot be read: no database.")
		return one(it)
	}
	ws, err := queues.LoadWorkers(ctx, e.h.DB, e.now)
	if err != nil {
		it := crit(base("workers", GroupWorkers, KindWorkers, "Workers"))
		it.set(StatusDegraded, "workers_unreadable", map[string]any{"detail": errText(err)},
			"The worker list cannot be read: "+errText(err))
		return one(it)
	}
	return workerItems(ws, runningByWorker(ctx, e.h.DB), e.now, e.rules)
}

func checkStreamingHosts(ctx context.Context, e *env) []Item {
	cl, err := e.tvCluster(ctx)
	if err != nil {
		if errors.Is(err, errNotWired) {
			return nil
		}
		it := base("streaming_hosts", GroupWorkers, KindStreamingHost, "Streaming machines")
		it.set(StatusDegraded, "streaming_hosts_unreadable", map[string]any{"detail": errText(err)},
			"The streaming machines cannot be read: "+errText(err))
		return one(it)
	}
	onlineNames := map[string]bool{}
	for _, x := range cl.Hosts {
		if x.State != "down" {
			onlineNames[strings.ToLower(x.Name)] = true
		}
	}
	out := []Item{}
	for _, x := range cl.Hosts {
		it := base("streaming_host:"+x.HostID, GroupWorkers, KindStreamingHost, x.Name)
		it.Link = "/admin/live-tv"
		age := time.Duration(-1)
		if x.LastBeatAt != nil {
			it.LastChecked = *x.LastBeatAt
			age = e.now.Sub(*x.LastBeatAt)
		}
		reason := ""
		if x.Reason != nil {
			reason = *x.Reason
		}
		it.Details = map[string]any{"host_id": x.HostID, "role": x.Role, "state": x.State, "used": x.Used,
			"capacity": x.Capacity, "encoders": x.Encoders, "channels": x.Channels, "version": x.Version}
		p := map[string]any{"used": x.Used, "capacity": x.Capacity, "detail": reason}
		if age >= 0 {
			p["age_seconds"] = secs(age)
		}
		switch x.State {
		case "online":
			it.set(StatusOK, "streaming_host_ok", p,
				fmt.Sprintf("Online; %d of %d channels.", x.Used, x.Capacity))
		case "disabled":
			it.set(StatusOK, "streaming_host_disabled", p, "Takes no channels: "+reason)
		case "draining":
			it.set(StatusOK, "streaming_host_draining", p, "Draining: takes no new channels.")
		case "unfit":
			it.set(StatusDegraded, "streaming_host_unfit", p, "Cannot take channels: "+reason)
		default: // down
			switch {
			case onlineNames[strings.ToLower(x.Name)]:
				it.History = true
				it.set(StatusOK, "streaming_host_replaced", p,
					"Replaced by a newer record of the same machine.")
			case age < 0 || age > e.rules.forget():
				it.History = true
				it.set(StatusOK, "streaming_host_forgotten", p, "Not seen for a long time; kept as history.")
			default:
				it.set(StatusDown, "streaming_host_down", p, "No heartbeat: "+reason)
			}
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return !out[i].History && out[j].History })
	return out
}

// ---------- dependencies ----------

const dependenciesSQL = `SELECT name, healthy, message, checked_at, since FROM dependency_health ORDER BY name`

func checkDependencies(ctx context.Context, e *env) []Item {
	if e.h.DB == nil {
		return nil
	}
	rows, err := e.h.DB.QueryContext(ctx, dependenciesSQL)
	if err != nil {
		it := base("dependencies", GroupDependencies, KindDependency, "Dependencies")
		it.set(StatusDegraded, "dependencies_unreadable", map[string]any{"detail": errText(err)},
			"The dependency checks cannot be read: "+errText(err))
		return one(it)
	}
	defer rows.Close()
	out := []Item{}
	stale := time.Duration(e.rules.DependencyStaleSeconds) * time.Second
	for rows.Next() {
		var name string
		var healthy bool
		var msg sql.NullString
		var checked, since flexTime
		if err := rows.Scan(&name, &healthy, &msg, &checked, &since); err != nil {
			continue
		}
		it := base("dependency:"+name, GroupDependencies, KindDependency, name)
		it.LastChecked = checked.t
		it.Link = "/queue/failures"
		it.Details = map[string]any{"message": msg.String, "since": since.ptr(), "checked_at": checked.ptr()}
		p := map[string]any{"detail": msg.String}
		age := e.now.Sub(checked.t)
		switch {
		case !healthy:
			if since.ok {
				p["since_seconds"] = secs(e.now.Sub(since.t))
			}
			it.set(StatusDown, "dependency_down", p, "Not available: "+msg.String)
		case !checked.ok || age > stale:
			p["age_seconds"] = secs(age)
			it.set(StatusDegraded, "dependency_stale", p,
				fmt.Sprintf("Not checked for %s: the workers that check it may be stopped.", ageText(age)))
		default:
			it.set(StatusOK, "dependency_ok", p, "Available: "+msg.String)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		it := base("dependencies", GroupDependencies, KindDependency, "Dependencies")
		it.set(StatusDegraded, "dependencies_unreadable", map[string]any{"detail": errText(err)},
			"The dependency checks cannot be read: "+errText(err))
		return append(out, it)
	}
	if len(out) == 0 {
		it := base("dependencies", GroupDependencies, KindDependency, "Dependencies")
		it.set(StatusOK, "dependencies_none", nil, "Nothing checked yet.")
		return one(it)
	}
	return out
}

// ---------- integrations ----------

func boolAt(m map[string]any, path ...string) (bool, bool) {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return false, false
		}
		cur = mm[k]
	}
	b, ok := cur.(bool)
	return b, ok
}

func numAt(m map[string]any, path ...string) (float64, bool) {
	var cur any = m
	for _, k := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur = mm[k]
	}
	f, ok := cur.(float64)
	return f, ok
}

func checkPlex(ctx context.Context, e *env) []Item {
	it := base("plex_livetv", GroupIntegrations, KindPlexLiveTV, "Plex Live TV")
	it.Link = "/admin/live-tv"
	if e.h.TV == nil {
		return nil
	}
	st, err := e.h.TV.ReadPlexStatus(ctx)
	if err != nil {
		it.set(StatusDegraded, "plex_unreadable", map[string]any{"detail": errText(err)},
			"Its state cannot be read: "+errText(err))
		return one(it)
	}
	if st.CheckedAt != nil {
		if t, ok := parseTime(*st.CheckedAt); ok {
			it.LastChecked = t
		}
	}
	lastErr := ""
	if st.LastError != nil {
		lastErr = *st.LastError
	}
	it.Details = map[string]any{"server": st.Plex.URL, "checked_at": st.CheckedAt,
		"last_error": st.LastError, "last_error_at": st.LastErrorAt}
	switch {
	case !st.Plex.Enabled || !st.Plex.Configured:
		it.set(StatusOK, "plex_off", nil, "Plex is not set up.")
		return one(it)
	case st.Stage != nil && !st.Stage.Enabled:
		it.set(StatusOK, "plex_stage_off", nil, "Plex Live TV is switched off.")
		return one(it)
	case st.CheckedAt == nil:
		it.set(StatusDegraded, "plex_never_checked", nil, "Not checked yet.")
		return one(it)
	}
	if reachable, ok := boolAt(st.Status, "reachable"); ok && !reachable {
		detail, _ := st.Status["error"].(string)
		if detail == "" {
			detail = lastErr
		}
		it.set(StatusDown, "plex_unreachable", map[string]any{"detail": detail},
			"Plex does not answer: "+detail)
		return one(it)
	}
	if reg, ok := boolAt(st.Status, "maktaba", "registered"); ok && !reg {
		it.set(StatusDegraded, "plex_not_registered", nil, "Maktaba's tuner is not registered in Plex.")
		return one(it)
	}
	lineup, _ := numAt(st.Status, "channels", "lineup")
	mapped, _ := numAt(st.Status, "channels", "mapped")
	p := map[string]any{"lineup": int(lineup), "mapped": int(mapped)}
	if sync, ok := boolAt(st.Status, "channels", "in_sync"); ok && !sync {
		it.set(StatusDegraded, "plex_out_of_sync", p,
			fmt.Sprintf("Channels out of sync: Plex holds %d of %d.", int(mapped), int(lineup)))
		return one(it)
	}
	if lastErr != "" && st.LastErrorAt != nil {
		if at, ok := parseTime(*st.LastErrorAt); ok && !at.Before(it.LastChecked) {
			it.set(StatusDegraded, "plex_error", map[string]any{"detail": lastErr}, "Last run failed: "+lastErr)
			return one(it)
		}
	}
	it.set(StatusOK, "plex_ok", p, fmt.Sprintf("Registered; %d channels in sync.", int(lineup)))
	return one(it)
}

// ---------- queues & TV ----------

func checkQueues(ctx context.Context, e *env) []Item {
	it := base("queues", GroupQueuesTV, KindQueues, "Queues")
	it.Link = "/queue"
	if e.h.Queues == nil {
		return nil
	}
	r, err := e.h.Queues.HealthRollup(ctx)
	if err != nil {
		it.set(StatusDegraded, "queues_unreadable", map[string]any{"detail": errText(err)},
			"Queue health cannot be read: "+errText(err))
		return one(it)
	}
	p := map[string]any{"stages": r.Stages, "failing": len(r.Failing), "waiting": len(r.Waiting),
		"needs_you": r.NeedsYou, "failing_stages": strings.Join(r.Failing, ", "),
		"waiting_stages": strings.Join(r.Waiting, ", ")}
	it.Details = map[string]any{"failing": r.Failing, "waiting": r.Waiting, "needs_you": r.NeedsYou}
	switch {
	case len(r.Failing) > 0:
		it.Link = "/queue/failures"
		it.set(StatusDegraded, "queues_failing", p,
			fmt.Sprintf("%d failing: %s.", len(r.Failing), strings.Join(r.Failing, ", ")))
	case len(r.Waiting) > 0:
		it.set(StatusDegraded, "queues_waiting", p,
			fmt.Sprintf("%d waiting on a dependency: %s.", len(r.Waiting), strings.Join(r.Waiting, ", ")))
	case r.NeedsYou > 0:
		it.Link = "/queue/failures"
		it.Attention = true
		it.set(StatusOK, "queues_needs_you", p, fmt.Sprintf("%d items need you.", r.NeedsYou))
	default:
		it.set(StatusOK, "queues_ok", p, fmt.Sprintf("All %d queues are fine.", r.Stages))
	}
	return one(it)
}

func checkTVChannels(ctx context.Context, e *env) []Item {
	it := base("tv_channels", GroupQueuesTV, KindTVChannels, "Channels on air")
	it.Link = "/admin/live-tv"
	cl, err := e.tvCluster(ctx)
	if err != nil {
		if errors.Is(err, errNotWired) {
			return nil
		}
		it.set(StatusDegraded, "tv_unreadable", map[string]any{"detail": errText(err)},
			"The channels on air cannot be read: "+errText(err))
		return one(it)
	}
	onAir, stale := 0, []string{}
	for _, c := range cl.OnAir {
		if c.Stale {
			stale = append(stale, c.Name)
		} else {
			onAir++
		}
	}
	p := map[string]any{"on_air": onAir, "stale": len(stale), "channels": strings.Join(stale, ", ")}
	it.Details = map[string]any{"on_air": onAir, "stale": stale, "failovers_24h": cl.Failovers24h}
	if len(stale) > 0 {
		it.set(StatusDegraded, "tv_channels_stale", p,
			fmt.Sprintf("%d channels stopped reporting: %s.", len(stale), strings.Join(stale, ", ")))
		return one(it)
	}
	it.set(StatusOK, "tv_channels_ok", p, fmt.Sprintf("%d channels on air.", onAir))
	return one(it)
}

const openChangesSQL = `SELECT id, number, state, approved_by FROM tv_changes
 WHERE state IN ('draft','staged','verifying','ready') ORDER BY number, created_at`

func checkTVChange(ctx context.Context, e *env) []Item {
	it := base("tv_change", GroupQueuesTV, KindTVChange, "TV change")
	it.Link = "/admin/live-tv/changes"
	if e.h.DB == nil {
		return nil
	}
	rows, err := e.h.DB.QueryContext(ctx, openChangesSQL)
	if err != nil {
		it.set(StatusDegraded, "tv_change_unreadable", map[string]any{"detail": errText(err)},
			"TV changes cannot be read: "+errText(err))
		return one(it)
	}
	defer rows.Close()
	for rows.Next() {
		var id, state string
		var number int64
		var appr []byte
		if rows.Scan(&id, &number, &state, &appr) != nil {
			continue
		}
		by := []string{}
		_ = json.Unmarshal(appr, &by)
		if len(by) > 0 {
			continue // approved: on its way to the air
		}
		it.Link = "/admin/live-tv/changes/" + id
		p := map[string]any{"number": number, "state": state}
		it.Details = map[string]any{"id": id, "number": number, "state": state}
		if state == "ready" {
			it.Attention = true
			it.set(StatusOK, "tv_change_ready", p,
				fmt.Sprintf("Change #%d is checked and waits for your approval.", number))
		} else {
			it.set(StatusOK, "tv_change_open", p, fmt.Sprintf("Change #%d is %s.", number, state))
		}
		return one(it)
	}
	it.set(StatusOK, "tv_change_none", nil, "No TV change waits for approval.")
	return one(it)
}

// slotUnhealthyAfter mirrors the TV slots' three missed beats (story 33.1).
const slotUnhealthyAfter = 15 * time.Second

func checkTVSlots(ctx context.Context, e *env) []Item {
	if e.h.DB == nil {
		return nil
	}
	single := base("tv_slot", GroupQueuesTV, KindTVSlot, "Live TV slot")
	single.Link = "/admin/live-tv"
	rows, err := e.h.DB.QueryContext(ctx,
		`SELECT host_id, slot, state, beat_at FROM tv_slots WHERE role = 'live' ORDER BY host_id, slot`)
	if err != nil {
		single.set(StatusOK, "tv_slot_single", nil, "The TV runs in one process.")
		return one(single)
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var host, slot, state string
		var beat flexTime
		if rows.Scan(&host, &slot, &state, &beat) != nil {
			continue
		}
		it := base("tv_slot:"+host+":"+slot, GroupQueuesTV, KindTVSlot, "Live TV slot "+slot)
		it.Link = "/admin/live-tv"
		it.LastChecked = beat.t
		p := map[string]any{"slot": slot, "host": host, "state": state}
		it.Details = map[string]any{"host_id": host, "slot": slot, "state": state, "beat_at": beat.ptr()}
		if state == "healthy" && beat.ok && e.now.Sub(beat.t) <= slotUnhealthyAfter {
			it.set(StatusOK, "tv_slot_ok", p, fmt.Sprintf("Slot %s is live and healthy.", slot))
		} else {
			if beat.ok {
				p["age_seconds"] = secs(e.now.Sub(beat.t))
			}
			what := "silent"
			if state != "healthy" {
				what = state
			}
			it.set(StatusDown, "tv_slot_unhealthy", p, fmt.Sprintf("Slot %s is live but %s.", slot, what))
		}
		out = append(out, it)
	}
	if len(out) == 0 {
		single.set(StatusOK, "tv_slot_single", nil, "The TV runs in one process.")
		return one(single)
	}
	return out
}

func checkTVTuner(ctx context.Context, e *env) []Item {
	it := base("tv_tuner", GroupQueuesTV, KindTVTuner, "TV tuner")
	it.Link = "/admin/live-tv"
	if e.h.TV == nil {
		return nil
	}
	d, err := e.h.TV.ReadDevice(ctx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		it.set(StatusOK, "tv_tuner_not_set_up", nil, "The TV tuner is not set up.")
		return one(it)
	case err != nil:
		it.set(StatusDegraded, "tv_tuner_unreadable", map[string]any{"detail": errText(err)},
			"The TV tuner cannot be read: "+errText(err))
		return one(it)
	case !d.Enabled:
		it.set(StatusOK, "tv_tuner_off", nil, "The TV tuner is off.")
		return one(it)
	}
	inUse := 0
	if cl, err := e.tvCluster(ctx); err == nil {
		for _, c := range cl.OnAir {
			inUse += c.Viewers.Tuners
		}
	}
	p := map[string]any{"in_use": inUse, "count": d.TunerCount}
	it.Details = map[string]any{"name": d.FriendlyName, "in_use": inUse, "count": d.TunerCount}
	if d.TunerCount > 0 && inUse >= d.TunerCount {
		it.set(StatusDegraded, "tv_tuner_full", p,
			fmt.Sprintf("Every tuner is in use (%d of %d): another viewer is refused.", inUse, d.TunerCount))
		return one(it)
	}
	it.set(StatusOK, "tv_tuner_ok", p, fmt.Sprintf("%d of %d tuners in use.", inUse, d.TunerCount))
	return one(it)
}

// ---------- time ----------

// flexTime scans a timestamp stored as a time or as text.
type flexTime struct {
	t  time.Time
	ok bool
}

func (f *flexTime) Scan(v any) error {
	switch x := v.(type) {
	case nil:
	case time.Time:
		f.t, f.ok = x, true
	case string:
		f.t, f.ok = parseTime(x)
	case []byte:
		f.t, f.ok = parseTime(string(x))
	}
	return nil
}

func (f flexTime) ptr() *time.Time {
	if !f.ok {
		return nil
	}
	t := f.t
	return &t
}

func parseTime(s string) (time.Time, bool) {
	for _, l := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// secs is a duration as whole seconds for a reason's params (the page
// formats it in the reader's language).
func secs(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return int64(d.Seconds())
}
