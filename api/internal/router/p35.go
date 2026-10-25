// Epic 35 (full-Maktaba cluster machines and automatic updates) API wiring, story 35.3:
// the home's update policy, release discovery (release_catalog, verified on sight) and
// the quiet-window scheduler, with the admin endpoints under /api/cluster/updates.
//
// The Epic 28 routes (p28.go) stay where they are; once this is mounted, GET
// /api/system/updates gains verified / policy / next_run_at, and POST
// /api/admin/system/update becomes Update now on builds that install by themselves
// (updates.AutoInstallAvailable).
package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
)

// HubRef is the cluster channel as the update driver reaches it: the hub is mounted
// after the update routes (mountCluster), so the driver reads it through this reference
// (nil until then: a run waits for its machines).
type HubRef struct {
	h atomic.Pointer[agent.Hub]
}

// Set fills the reference.
func (r *HubRef) Set(h *agent.Hub) {
	if r != nil {
		r.h.Store(h)
	}
}

// Get is the hub (nil before it is set).
func (r *HubRef) Get() *agent.Hub {
	if r == nil {
		return nil
	}
	return r.h.Load()
}

// hubCluster is updates.Cluster over the hub.
type hubCluster struct{ ref *HubRef }

func (c hubCluster) hub() *agent.Hub {
	if c.ref == nil {
		return nil
	}
	return c.ref.h.Load()
}

func (c hubCluster) MachineAgents() map[string]string {
	if h := c.hub(); h != nil {
		return h.MachineAgents()
	}
	return map[string]string{}
}

func (c hubCluster) MachineStates() map[string]json.RawMessage {
	if h := c.hub(); h != nil {
		return h.MachineStates()
	}
	return map[string]json.RawMessage{}
}

// NodeHosts implements the updates driver's nodeHoster: the hosts a machine's sessions
// report, to find its running jobs.
func (c hubCluster) NodeHosts() map[string][]string {
	if h := c.hub(); h != nil {
		return h.NodeHosts()
	}
	return map[string][]string{}
}

// NodeWorkers implements the updates driver's nodeWorkerer: each machine's pipeline
// worker, where a worker-only machine's conversion goes (update.convert).
func (c hubCluster) NodeWorkers() map[string]updates.NodeWorker {
	out := map[string]updates.NodeWorker{}
	if h := c.hub(); h != nil {
		for id, w := range h.NodeWorkers() {
			out[id] = updates.NodeWorker{Session: w.Session, Version: w.Version}
		}
	}
	return out
}

func (c hubCluster) HomeProcesses() []string {
	if h := c.hub(); h != nil {
		return h.HomeProcesses()
	}
	return nil
}

func (c hubCluster) Command(ctx context.Context, sid, name string, args any, wait time.Duration) (bool, json.RawMessage, error) {
	h := c.hub()
	if h == nil {
		return false, nil, agent.ErrNoSession
	}
	b, err := json.Marshal(args)
	if err != nil {
		return false, nil, err
	}
	res, err := h.CommandWithin(ctx, sid, name, b, "updates", wait)
	return res.OK, res.Detail, err
}

// UpdatesRef is the Epic 35 update handler as the Epic 28 routes see it: p28 mounts
// before the application database is open, so it reads the handler through this
// reference at request time (nil: the Epic 28 behaviour).
type UpdatesRef struct {
	h atomic.Pointer[updates.Handler]
}

// Get is the mounted handler, or nil.
func (u *UpdatesRef) Get() *updates.Handler {
	if u == nil {
		return nil
	}
	return u.h.Load()
}

// P35Deps bundles the dependencies of the Epic 35 update handlers.
type P35Deps struct {
	DB     *sql.DB
	Logger *slog.Logger
	// Current is the running version, BaseChannel the build's channel.
	Current     string
	BaseChannel string
	// Verifier checks a release's signed files (story 35.1); nil: updates.Unwired
	// (nothing verifies, nothing installs).
	Verifier updates.Verifier
	// Notifier is the notification hub (nil: a hub over DB).
	Notifier notify.Notifier
	// Ref, when set, is filled with the handler for the Epic 28 routes.
	Ref *UpdatesRef
	// RunLoops starts the GitHub poller (every 6 h) and the scheduler (every minute).
	RunLoops bool
	Ctx      context.Context
	// Getenv is the environment (nil: os.Getenv).
	Getenv func(string) string
	// GitHubBaseURL overrides the GitHub API base (tests).
	GitHubBaseURL string
	// HTTPClient for GitHub and the supervisor (nil: defaults).
	HTTPClient *http.Client
	// Hub is the cluster channel, filled once mounted (nil: a run updates the home only).
	Hub *HubRef
	// Home is the home's supervisor (nil: MAKTABA_SUPERVISOR_URL and its token file).
	Home updates.HomeSupervisor
	// DataDir is the API's data volume: the release mirror keeps its copies there
	// (<data_dir>/updates/mirror, story 35.12 AC3); "" keeps none (the machines download
	// from GitHub and PyPI themselves).
	DataDir string
}

// homeReleaseCopies are the folders of a release the home staged itself (a side-by-side
// layout: the launcher runs it from bin/releases/<v>, MAKTABA_RELEASE_DIR): its signed
// files, and its pipeline's wheel and constraints. Nil on another kind of install.
func homeReleaseCopies(releaseDir string) func(version string) []string {
	releaseDir = strings.TrimSpace(releaseDir)
	if releaseDir == "" {
		return nil
	}
	releases := filepath.Dir(filepath.Clean(releaseDir))
	return func(version string) []string {
		dir := filepath.Join(releases, updates.NormalizeVersion(version))
		return []string{dir, filepath.Join(dir, "pipeline")}
	}
}

// MountP35 attaches the update routes and starts the loops.
func MountP35(r chi.Router, d P35Deps) *updates.Handler {
	if d.DB == nil {
		return nil
	}
	getenv := d.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	hub := d.Notifier
	if hub == nil {
		hub = notify.NewHub(d.DB, d.Logger)
	}
	store := &updates.Store{DB: d.DB}
	svc := &updates.Service{Store: store, Notifier: hub, Log: d.Logger, Current: d.Current,
		BaseChannel: d.BaseChannel, Getenv: getenv}
	disabled := func() bool {
		v := getenv("MAKTABA_UPDATE_DISABLE")
		return v == "1" || strings.EqualFold(v, "true")
	}()
	if !disabled {
		svc.Discoverer = &updates.Discoverer{Store: store, Verifier: d.Verifier, Notifier: hub, Log: d.Logger,
			HTTP: d.HTTPClient, BaseURL: d.GitHubBaseURL, Repo: getenv("MAKTABA_UPDATE_REPO"),
			Token: getenv("MAKTABA_GITHUB_TOKEN"), Policy: svc.Policy, Current: d.Current}
	}
	sup := getenv("MAKTABA_SUPERVISOR_URL")
	home := d.Home
	if home == nil {
		home = &updates.SupervisorClient{BaseURL: sup, TokenFile: getenv("MAKTABA_SUPERVISOR_TOKEN_FILE"), HTTP: d.HTTPClient}
	}
	// the cluster's runs (stories 35.4, 35.12): the home's supervisor, then the machines
	drv := &updates.Driver{Store: store, Svc: svc, Home: home, Cluster: hubCluster{ref: d.Hub}, Log: d.Logger,
		Getenv: getenv,
		QuietMaxViewers: func(ctx context.Context) int {
			cfg, err := tvchange.LoadConfig(ctx, d.DB)
			if err != nil {
				return 0
			}
			return cfg.QuietMaxViewers
		}}
	if d.DataDir != "" {
		// the machines' downloads: from the home first (the wheelhouse with the pipeline's
		// Python the supervisor names)
		drv.Mirror = &updates.Mirror{Store: store, Dir: filepath.Join(d.DataDir, "updates", "mirror"), Log: d.Logger,
			Python: func() string { return getenv("MAKTABA_WHEELHOUSE_PYTHON") },
			Enabled: func(ctx context.Context) bool {
				p, err := svc.Policy(ctx)
				return err != nil || p.Wheelhouse
			},
			Local: homeReleaseCopies(getenv("MAKTABA_RELEASE_DIR"))}
	}
	svc.Starter = drv
	// story 35.15: the update events in the hub's events file, the numbers on /metrics
	drv.Events = func(fields map[string]any) {
		if h := d.Hub.Get(); h != nil {
			h.Record(fields)
		}
	}
	upMetrics := updates.NewMetrics(drv, func() string { return d.Current })
	updates.Expose(upMetrics)
	svc.Blockers = []updates.Blocker{
		updates.TVChangeBlocker{DB: d.DB},
		updates.SwitchoverBlocker{DB: d.DB},
		&updates.HomeHealth{BaseURL: sup, HTTP: d.HTTPClient},
	}
	rulesHandler := &rules.Handler{DB: d.DB, Staging: tvchange.NewDrafts(d.DB)}
	h := &updates.Handler{Svc: svc, Driver: drv, RulesPatch: rulesHandler.Patch, Mirror: drv.Mirror}
	h.Mount(r)
	if d.Ref != nil {
		d.Ref.h.Store(h)
	}
	if d.RunLoops {
		ctx := d.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		interval := updates.CheckInterval
		if v := strings.TrimSpace(getenv("MAKTABA_UPDATE_INTERVAL_SEC")); v != "" {
			if n, err := time.ParseDuration(v + "s"); err == nil {
				interval = n // 0: no background checks (Epic 28 meaning), "check now" still works
			}
		}
		if svc.Discoverer != nil && interval > 0 {
			go svc.Discoverer.Run(ctx, interval)
		}
		go svc.RunScheduler(ctx)
		go drv.Run(ctx)
		sampler := &updates.HealthSampler{Metrics: upMetrics, Logger: d.Logger, Behind: func(ctx context.Context) bool {
			pl, _, _, err := svc.Plan(ctx)
			return err == nil && pl.Target != nil
		}}
		go sampler.Run(ctx)
	}
	return h
}
