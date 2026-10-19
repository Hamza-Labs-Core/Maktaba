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
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
)

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
	svc.Starter = &updates.SupervisorClient{BaseURL: sup, Token: getenv("MAKTABA_SUPERVISOR_TOKEN"), HTTP: d.HTTPClient}
	svc.Blockers = []updates.Blocker{
		updates.TVChangeBlocker{DB: d.DB},
		&updates.HomeHealth{BaseURL: sup, HTTP: d.HTTPClient},
	}
	rulesHandler := &rules.Handler{DB: d.DB, Staging: tvchange.NewDrafts(d.DB)}
	h := &updates.Handler{Svc: svc, RulesPatch: rulesHandler.Patch}
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
	}
	return h
}
