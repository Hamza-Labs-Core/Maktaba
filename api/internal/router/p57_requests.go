// Epic 57 stories 57.06 and 57.08 wiring: ask a parent, the parent PIN, and a
// child's activity for its guardians.
//
//	POST /api/me/parental/requests                    home, a restricted account
//	GET  /api/me/parental/requests                    home, a restricted account (its own requests)
//	GET  /api/me/parental/guardians                   home, a restricted account
//	POST /api/me/parental/unlock                      home, a restricted account
//	GET  /api/me/parental-pin                         home, admin or a guardian
//	PUT  /api/me/parental-pin                         home, admin or a guardian
//	GET  /api/parental/requests                       home, admin or a guardian
//	POST /api/parental/requests/{id}/approve|deny     home, admin or that child's guardian
//	GET  /api/users/{id}/parental/activity            home, admin or that child's guardian
package router

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
)

// P57RequestsDeps bundles what the routes need.
type P57RequestsDeps struct {
	DB *sql.DB
	// Cache is the restricted accounts' cache (nil: the routes read the database).
	Cache *parental.Cache
	// Time is the time keeper (story 57.14): told when an approved request for
	// more time gave a grant, so a running stream picks it up at once (nil: the
	// stream learns of it at its next heartbeat).
	Time     *parental.TimeKeeper
	Notifier notify.Notifier
	Loops    *runner.Runner
	Logger   *slog.Logger
}

// MountP57Requests mounts the routes (nil DB: nothing) and declares the
// parental_activity loop.
func MountP57Requests(r chi.Router, d P57RequestsDeps) {
	if d.DB == nil {
		return
	}
	store := parental.NewStore(d.DB)
	if d.Cache != nil {
		store = d.Cache.Store()
	}
	// story 57.12: a child's device that signs in before parental controls are set up
	NotifyChildDevices(store, d.Notifier, d.Logger)
	accounts := users.New(d.DB)
	h := &parentalh.AskHandler{
		Handler: parentalh.Handler{Store: store, Cache: d.Cache, Time: d.Time},
		Notify:  d.Notifier,
		Logger:  d.Logger,
		VerifyPassword: func(ctx context.Context, id, pw string) error {
			u, err := accounts.GetByID(ctx, id)
			if err != nil {
				return err
			}
			return u.VerifyPassword(pw)
		},
	}
	h.Mount(r)
	if d.Loops != nil {
		d.Loops.Declare("parental_activity", runner.Func(func(ctx context.Context) {
			RunParentalActivity(ctx, store, d.Logger, time.Minute)
		}))
	}
}

// RunParentalActivity is the parental_activity loop: every tick it writes the
// watched events of the plays that got far enough and closes the requests no one
// needs to answer; once a day (and when it starts) it deletes the events older
// than 90 days. It returns when ctx ends.
func RunParentalActivity(ctx context.Context, store *parental.Store, logger *slog.Logger, every time.Duration) {
	if logger == nil {
		logger = slog.Default()
	}
	t := time.NewTicker(every)
	defer t.Stop()
	var swept time.Time
	for {
		ParentalActivityOnce(ctx, store, logger, &swept)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ParentalActivityOnce is one pass of the loop. swept is when the daily delete
// last ran.
func ParentalActivityOnce(ctx context.Context, store *parental.Store, logger *slog.Logger, swept *time.Time) {
	if _, err := store.RecordWatched(ctx); err != nil && ctx.Err() == nil {
		logger.Warn("parental: watched events", "event", "parental_activity_failed", "step", "watched", "error", err.Error())
	}
	if _, err := store.CloseRequests(ctx); err != nil && ctx.Err() == nil {
		logger.Warn("parental: closing requests", "event", "parental_activity_failed", "step", "requests", "error", err.Error())
	}
	if now := store.Clock(); swept.IsZero() || now.Sub(*swept) >= 24*time.Hour {
		n, err := store.SweepEvents(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("parental: deleting old events", "event", "parental_activity_failed", "step", "sweep", "error", err.Error())
			}
			return
		}
		*swept = now
		if n > 0 {
			logger.Info("parental: old events deleted", "event", "parental_events_swept", "deleted", n)
		}
	}
}
