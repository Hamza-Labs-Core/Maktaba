package router

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	discoveryh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/discovery"
	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
)

// ParentalHandles is what Epic 57's mounts built, kept by main.go for the stories
// that filter or re-rate with them: the restricted accounts' cache (MountParental,
// through P6Deps.OnParental) and the age tiers' recompute (MountP57Tiers). Either is
// nil where its mount did not run.
type ParentalHandles struct {
	Cache     *parental.Cache
	Recompute *parental.Recomputer
	// Time is the time keeper (story 57.14).
	Time *parental.TimeKeeper
}

// MountParental mounts Epic 57 story 57.02's routes: restricted accounts, their
// guardians and their rules. With the bus's Postgres listener (busCtx and busDSN
// set) a policy is read from memory and dropped on slot 0221's parental_policy
// notification, so a tightened limit holds on the next request on every process;
// without it every request reads the database. It returns the cache the other
// surfaces filter with.
func MountParental(busCtx context.Context, r chi.Router, db *sql.DB, loops *runner.Runner, busDSN string, resettle func(context.Context) string) *parental.Cache {
	cache := parental.NewCache(parental.NewStore(db))
	if busCtx != nil && busDSN != "" {
		loops.Declare("parental_cache_listen", runner.Bound(busCtx, runner.Func(func(ctx context.Context) {
			cache.Listen(ctx, busDSN, resettle, slog.Default())
		})))
	}
	// Each single item the filter refuses a restricted account (story 57.03) is
	// one `blocked` event in the child's activity (story 57.08); the store keeps
	// one per subject per day. The write outlives a request the client dropped.
	parental.SetBlockedHook(func(ctx context.Context, e *parental.Effective, s parental.Subject) {
		if err := cache.Store().RecordBlocked(context.WithoutCancel(ctx), e.UserID, s.Kind, s.ID); err != nil {
			slog.Default().Error("parental: record blocked event", "err", err)
		}
	})
	// A looser signal that waits for a guardian is shown in the child's activity
	// (story 57.08). The devices table keeps no name, so the event carries the
	// sign-in's platform, or "device" when the sign-in has no registered device.
	cache.Store().OnSignalPending = func(ctx context.Context, userID, family string) {
		name := "device"
		var platform sql.NullString
		if err := db.QueryRowContext(ctx,
			`SELECT d.platform FROM refresh_tokens rt JOIN devices d ON d.id = rt.device_id
			  WHERE rt.user_id = $1 AND rt.family_id = $2 ORDER BY rt.issued_at DESC LIMIT 1`,
			userID, family).Scan(&platform); err == nil && platform.String != "" {
			name = platform.String
		}
		if err := cache.Store().RecordSignalEvent(ctx, userID, name); err != nil {
			slog.Default().Error("parental: record signal event", "err", err)
		}
	}
	h := &parentalh.Handler{Store: cache.Store(), Cache: cache}
	h.Mount(r)
	h.MountSignal(r)   // story 57.04: the device's signal
	h.MountSettings(r) // story 57.12: turning it on in an existing home
	return cache
}

// approveAs adapts parental.Store.ApproveAs to the pairing handler's seam.
func approveAs(db *sql.DB) func(context.Context, *principal.Principal, string) (string, error) {
	store := parental.NewStore(db)
	return func(ctx context.Context, p *principal.Principal, asUser string) (string, error) {
		id, err := store.ApproveAs(ctx, p, asUser)
		switch {
		case errors.Is(err, parental.ErrApproveForbidden):
			return "", discoveryh.ErrApproveAsRestricted
		case errors.Is(err, parental.ErrNotRestricted), errors.Is(err, parental.ErrUserNotFound):
			return "", discoveryh.ErrApproveAsNotFound
		}
		return id, err
	}
}

// ParentalCapabilities is P51Deps.Parental over the restricted accounts' cache: the
// caller's `parental` block of GET /api/capabilities (story 57.12). Nil without a
// cache.
func ParentalCapabilities(cache *parental.Cache) func(r *http.Request, p *principal.Principal) (any, error) {
	if cache == nil {
		return nil
	}
	return func(r *http.Request, p *principal.Principal) (any, error) {
		return parentalh.Capabilities(r, cache, p)
	}
}

// NoticeChildDevice is the kind of the notice admins get when a device says a child
// may be using it while parental controls are not turned on (story 57.12). The web
// shows it as `notify.parental.child_device`, linking to Family.
const NoticeChildDevice = "parental.child_device"

// childDeviceEvery keeps the notice to one per sign-in: a repeat about the same
// sign-in is dropped for as long as the notice is kept.
const childDeviceEvery = 10 * 365 * 24 * time.Hour

// NotifyChildDevices tells the admins, once per sign-in, that a child's device signed
// in before parental controls were set up. The notice names no account and no device.
func NotifyChildDevices(store *parental.Store, n notify.Notifier, logger *slog.Logger) {
	if store == nil || n == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	store.OnChildDevice = func(ctx context.Context, _, family string) {
		err := n.Notify(context.WithoutCancel(ctx), NoticeChildDevice, notify.Payload{
			Link: "/family", Subject: "sign_in:" + family, Every: childDeviceEvery})
		if err != nil {
			logger.Warn("parental: the child-device notice was not made", "event", "parental_notice_failed", "error", err.Error())
		}
	}
}
