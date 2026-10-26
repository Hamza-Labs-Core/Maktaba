// Epic 32 story 32.6 wiring: the home's sealed secret store, the one-time
// move of extension secrets out of extensions.settings, and the
// Cloudflare Stream integration (Test connection on the extensions page,
// /api/tv/remote admin routes). Kept apart from the 32.1-32.5 cluster
// wiring.
package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tv"
	tvremoteh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
)

// OpenHomeSecrets loads (or creates) the key file and returns the store,
// then moves any extension secret still in extensions.settings into it
// (AC6a; idempotent, so it runs at every start). A key that cannot be
// loaded leaves a store without a key: secrets can be listed and deleted
// but not opened or saved, and the move waits for the next start.
func OpenHomeSecrets(ctx context.Context, db *sql.DB, keyPath string, logger *slog.Logger) *secrets.DBStore {
	if logger == nil {
		logger = slog.Default()
	}
	store := &secrets.DBStore{DB: db}
	key, err := secrets.LoadOrCreateKey(keyPath)
	if err != nil {
		logger.Warn("p32: secrets key unavailable; extension secrets cannot be opened or saved",
			"path", keyPath, "err", err, "event", "p32_secrets_key_failed")
		return store
	}
	store.Key = key
	rep, err := secrets.MigrateExtensionSecrets(ctx, store, queues.ExtensionSecretFields())
	if err != nil {
		logger.Warn("p32: moving extension secrets to home_secrets failed; retried at the next start",
			"err", err, "event", "p32_secrets_move_failed")
		return store
	}
	if len(rep.Moved)+len(rep.Dropped) > 0 {
		logger.Info("p32: extension secrets moved to home_secrets",
			"moved", strings.Join(rep.Moved, ","), "dropped_stale", strings.Join(rep.Dropped, ","),
			"event", "p32_secrets_moved")
	}
	return store
}

// P32RemoteDeps bundles the integration's dependencies.
type P32RemoteDeps struct {
	DB      *sql.DB
	Secrets secrets.Store
	Logger  *slog.Logger
	// NoLoop: do not start the remote controller's loop (tests).
	NoLoop bool
	// Notifier receives the budget and simulcast-cap notices (nil: a hub over
	// DB, whose first subscriber is the notifications table the bell reads).
	Notifier notify.Notifier
}

// WireP32Remote builds the Cloudflare Stream integration over the
// extensions handler q (its Test connection and the account-change guard)
// and returns the remote controller (for /api/tv/info, story 32.9) and a
// mount function for the /api/tv/remote routes.
func WireP32Remote(q *queues.Handler, d P32RemoteDeps) (*tvremote.Integration, *tvremote.Controller, func(chi.Router)) {
	in := &tvremote.Integration{DB: d.DB, Secrets: d.Secrets, Ext: q, Logger: d.Logger}
	// Epic 32 stories 32.7-32.10: the remote controller (input pool, pushes,
	// sessions, tokens, budget) and its reconcile loop.
	ctl := &tvremote.Controller{DB: d.DB, Integration: in, Logger: d.Logger, Audit: remoteAudit(d.DB, d.Logger),
		Notifier: d.Notifier}
	if ctl.Notifier == nil && d.DB != nil {
		ctl.Notifier = notify.NewHub(d.DB, d.Logger)
	}
	in.StopPushes = ctl.StopAll
	in.ForgetInputs = ctl.Forget
	// Epic 34: the public site's state, posted outward to its Worker
	pub := &tvremote.Publisher{Ctl: ctl}
	if d.DB != nil && !d.NoLoop {
		go ctl.Run(context.Background())
		go pub.Run(context.Background())
		// story 32.11: the TV numbers the control centre charts, every minute
		go (&tvremote.HealthSampler{DB: d.DB, Remote: ctl, Logger: d.Logger}).Run(context.Background())
	}
	if q.Testers == nil {
		q.Testers = map[string]queues.ExtensionTester{}
	}
	if q.Validators == nil {
		q.Validators = map[string]queues.ExtensionValidator{}
	}
	q.Testers[queues.CloudflareExtension] = func(ctx context.Context) (any, error) {
		return in.Test(ctx)
	}
	q.Validators[queues.CloudflareExtension] = in.ValidateSettings
	WireExtensionTesters(q, d.DB, d.Secrets, d.Logger)
	return in, ctl, func(r chi.Router) { (&tvremoteh.Handler{Integration: in, Remote: ctl, Publisher: pub}).Mount(r) }
}

// remoteAudit writes remote tunes, ends and key rotations to audit_log
// (category tv_remote; story 32.8 AC9). Best effort: a miss is logged.
func remoteAudit(db *sql.DB, logger *slog.Logger) func(ctx context.Context, action, actor, target string, payload map[string]any) {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, action, actor, target string, payload map[string]any) {
		if db == nil {
			return
		}
		var who any
		if _, err := uuid.Parse(actor); err == nil {
			who = actor
		}
		blob, _ := json.Marshal(payload)
		if _, err := db.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
			VALUES ('tv_remote', $1, $2, $3, $4)`, action, who, target, string(blob)); err != nil {
			logger.Warn("tv_remote: audit row not written", "action", action, "err", err, "event", "tv_remote_audit_failed")
		}
	}
}

// RemoteAvailability adapts the controller to /api/tv/info (story 32.9); a
// nil controller: remote viewing is not available on this server.
func RemoteAvailability(ctl *tvremote.Controller) func(ctx context.Context) tv.RemoteAvailability {
	if ctl == nil {
		return nil
	}
	return func(ctx context.Context) tv.RemoteAvailability {
		a := ctl.Availability(ctx)
		return tv.RemoteAvailability{Available: a.Available, Reason: a.Reason, MaxStreams: a.MaxStreams,
			TokenTTLMinutes: a.TokenTTLMinutes, Detection: a.Detection}
	}
}
