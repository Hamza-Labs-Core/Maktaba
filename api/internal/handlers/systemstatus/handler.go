package systemstatus

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tv"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvcluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// QueueHealth is Epic 40's tile health rollup (queues.Handler).
type QueueHealth interface {
	HealthRollup(ctx context.Context) (queues.HealthRollup, error)
}

// TVCluster is the streaming machines and channels on air (tvcluster.Handler).
type TVCluster interface {
	Read(ctx context.Context) (tvcluster.Response, error)
}

// TVInfo is Plex Live TV and the emulated tuner (tv.Handler).
type TVInfo interface {
	ReadPlexStatus(ctx context.Context) (tv.PlexStatus, error)
	ReadDevice(ctx context.Context) (tv.Device, error)
}

// Handler answers GET /api/admin/system/status.
type Handler struct {
	DB        *sql.DB
	Version   string
	StartTime time.Time
	// StreamingURL is the streaming service's HTTP base
	// (MAKTABA_STREAMING_HTTP_URL); StreamingAddr its gRPC address
	// (MAKTABA_STREAMING_ADDR); PipelineAddr the pipeline's gRPC address
	// (MAKTABA_PIPELINE_ADDR). Empty: not configured.
	StreamingURL  string
	StreamingAddr string
	PipelineAddr  string
	// SupervisorURL is maktaba-server's supervisor ($MAKTABA_SUPERVISOR_URL);
	// empty: the processes are not supervised.
	SupervisorURL string

	Queues    QueueHealth
	TVCluster TVCluster
	TV        TVInfo

	// HTTP probes the streaming service and the supervisor; nil: a default
	// client (each request carries the check's deadline).
	HTTP *http.Client
	// Dial opens the gRPC addresses' TCP connection; nil: net.Dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	Now  func() time.Time
	// Rules overrides rules.system_health (tests); nil reads app_settings.
	Rules *Rules
	// ReadDir lists a library folder (host access); nil opens it and reads
	// one name. Stat answers whether a path exists; nil os.Stat.
	ReadDir func(path string) error
	Stat    func(path string) error

	probes probes // the folders' reads in flight (host access)

	mu       sync.Mutex // guards cached / cachedAt
	cached   *Report
	cachedAt time.Time
	run      sync.Mutex // one round of checks at a time
}

// Mount wires the route (admin only).
func (h *Handler) Mount(r chi.Router) {
	r.With(authmw.RequireAdmin).Get("/api/admin/system/status", h.Get)
}

// Get implements GET /api/admin/system/status.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	common.WriteJSON(w, r, http.StatusOK, h.Report(r.Context()))
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Report is the current answer: the cached one while it is fresh, else a
// new round of checks (one at a time: callers that arrive meanwhile get it).
func (h *Handler) Report(ctx context.Context) Report {
	if rep, ok := h.fresh(); ok {
		return rep
	}
	h.run.Lock()
	defer h.run.Unlock()
	if rep, ok := h.fresh(); ok {
		return rep
	}
	// the round is shared by every caller: it runs on its own deadlines, not
	// the first caller's request
	rep := h.compute(context.WithoutCancel(ctx))
	h.mu.Lock()
	h.cached, h.cachedAt = &rep, h.now()
	h.mu.Unlock()
	return rep
}

func (h *Handler) fresh() (Report, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cached == nil || h.now().Sub(h.cachedAt) >= h.cached.Rules.cache() {
		return Report{}, false
	}
	return *h.cached, true
}

func (h *Handler) rules(ctx context.Context) Rules {
	if h.Rules != nil {
		return *h.Rules
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return loadRules(ctx, h.DB)
}

// check is one part of the round: Run answers its items; Fallback is what
// is shown when it does not answer before the deadline (or panics).
type check struct {
	Name     string
	Fallback Item
	Run      func(ctx context.Context, e *env) []Item
}

// env is what the checks of one round share.
type env struct {
	h     *Handler
	now   time.Time
	rules Rules

	clusterOnce sync.Once
	cluster     *tvcluster.Response
	clusterErr  error
}

// tvCluster reads the streaming machines and channels on air once a round.
func (e *env) tvCluster(ctx context.Context) (*tvcluster.Response, error) {
	e.clusterOnce.Do(func() {
		if e.h.TVCluster == nil {
			e.clusterErr = errNotWired
			return
		}
		resp, err := e.h.TVCluster.Read(ctx)
		if err != nil {
			e.clusterErr = err
			return
		}
		e.cluster = &resp
	})
	return e.cluster, e.clusterErr
}

var errNotWired = fmt.Errorf("not available on this server")

func (h *Handler) compute(ctx context.Context) Report {
	now := h.now()
	e := &env{h: h, now: now, rules: h.rules(ctx)}
	items := runChecks(ctx, e, h.checks(), e.rules.timeout())
	sortItems(items)
	rep := Report{CheckedAt: now, Items: items, Rules: e.rules, Processes: h.SupervisorURL != ""}
	rep.Status, rep.Code, rep.Params, rep.Reason = rollup(items)
	return rep
}

// runChecks runs every check at the same time, each under timeout. A check
// still running at its deadline is reported by its fallback, marked as not
// answering; a check that panics the same way.
func runChecks(ctx context.Context, e *env, checks []check, timeout time.Duration) []Item {
	results := make([][]Item, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(i int, c check) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			done := make(chan []Item, 1)
			go func() {
				defer func() {
					if p := recover(); p != nil {
						safego.Log(nil, "systemstatus_check", p)
						done <- []Item{notAnswering(c.Fallback, e.now, fmt.Sprint(p))}
					}
				}()
				done <- c.Run(cctx, e)
			}()
			select {
			case got := <-done:
				for j := range got {
					if got[j].LastChecked.IsZero() {
						got[j].LastChecked = e.now
					}
				}
				results[i] = got
			case <-cctx.Done():
				results[i] = []Item{notAnswering(c.Fallback, e.now,
					fmt.Sprintf("no answer within %d ms", timeout.Milliseconds()))}
			}
		}(i, c)
	}
	wg.Wait()
	out := []Item{}
	for _, r := range results {
		out = append(out, r...)
	}
	return out
}

// notAnswering is a check's fallback item: down when the item is critical
// (a database that does not answer is down), else degraded.
func notAnswering(fb Item, now time.Time, detail string) Item {
	it := fb
	st := StatusDegraded
	if fb.Critical {
		st = StatusDown
	}
	it.set(st, "check_timeout", map[string]any{"detail": detail},
		"Did not answer in time: "+detail)
	it.LastChecked = now
	return it
}

var groupOrder = map[string]int{
	GroupServices: 0, GroupWorkers: 1, GroupDependencies: 2, GroupIntegrations: 3, GroupQueuesTV: 4,
}

// sortItems keeps the checks' own order inside a group (summary first) and
// the groups in page order.
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		return groupOrder[items[i].Group] < groupOrder[items[j].Group]
	})
}

// rollup is the overall rule: down when a critical item is down; degraded
// when anything else is not ok; else ok. History items never count.
func rollup(items []Item) (status, code string, params map[string]any, reason string) {
	var down, bad []string
	for _, it := range items {
		if it.History || it.Status == StatusOK {
			continue
		}
		bad = append(bad, it.Name)
		if it.Critical && it.Status == StatusDown {
			down = append(down, it.Name)
		}
	}
	switch {
	case len(down) > 0:
		return StatusDown, "overall_down", map[string]any{"items": strings.Join(down, ", ")},
			"Down: " + strings.Join(down, ", ")
	case len(bad) > 0:
		return StatusDegraded, "overall_degraded",
			map[string]any{"count": len(bad), "items": strings.Join(bad, ", ")},
			fmt.Sprintf("%d need attention: %s", len(bad), strings.Join(bad, ", "))
	}
	return StatusOK, "overall_ok", nil, "Everything is working."
}

// set fills an item's status and reason.
func (it *Item) set(status, code string, params map[string]any, reason string) {
	it.Status, it.Code, it.Params, it.Reason = status, code, params, reason
}
