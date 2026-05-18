// Phase 10 handler wiring — the four packages that previously had
// working Mount() methods but no caller. The audit
// (specs/FULL_IMPLEMENTATION_AUDIT.md §A.4) flagged them as
// "implementation present but unreachable"; this file wires them
// into the chi router so the routes actually respond.
//
// Coverage:
//
//   - subscriptions.Handler (Stories 16.1–16.6) — entitlements + admin license.
//   - discovery.Handler     (Stories 15.5/15.6) — pairing tickets.
//   - security.Handler      (Story  10.16)      — security.txt + SBOM.
//   - perf.Handler          (Epic   18 admin)   — cache flush / budgets.
package router

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/refresh"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	discoverypkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	discoveryh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/discovery"
	perfh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/perf"
	securityh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/security"
	subh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/subscriptions"
	perfpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
	securitypkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/security"
	subpkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// P10Deps bundles the dependencies for Phase 10 handlers. All fields
// have zero-value-safe defaults so callers can opt out by leaving a
// field unset.
type P10Deps struct {
	// DB enables the entitlement and pairing handlers to persist state.
	// Nil DB is permitted; handlers fall back to in-memory stores.
	DB *sql.DB

	// SubscriptionsStore is the runtime entitlements cache. Nil means
	// the package creates a default (free-tier) store.
	SubscriptionsStore *subpkg.Store

	// SubscriptionsVerifier verifies signed licenses. Nil leaves the
	// /api/admin/license endpoint disabled.
	SubscriptionsVerifier *subpkg.Verifier

	// PairingStore persists pairing tickets. Nil + a non-nil DB →
	// Postgres-backed store (`pairing_tickets`, slot 0055) so a code
	// survives a restart and works across replicas. Nil + nil DB →
	// in-memory store (dev-only).
	PairingStore discoverypkg.PairingStore

	// PairingTTL bounds ticket lifetime. Zero defaults to 5 minutes.
	PairingTTL time.Duration

	// Keys is the RS256 signing set. When set (with a DB), pairing
	// Exchange mints a real device access JWT + refresh token. Nil
	// leaves Exchange disabled (503) rather than dead-ending with an
	// unusable body.
	Keys *keys.Set

	// SecurityPolicy is the RFC 9116 disclosure policy. Zero-value
	// uses the package default.
	SecurityPolicy securitypkg.DisclosurePolicy

	// SecuritySBOM is the parsed SBOM published at /api/system/sbom.
	// Nil means the endpoint returns 503.
	SecuritySBOM *securitypkg.SBOM

	// PerfRegistry is the cache registry used by /admin/cache/.../flush.
	// Nil disables the flush endpoint.
	PerfRegistry *perfpkg.Registry

	// PerfBudgets is the parsed perf-budgets manifest. Nil disables
	// /api/admin/perf/budgets.
	PerfBudgets *perfpkg.Budgets

	// Logger surfaces boot-time breadcrumbs (e.g. a persisted license
	// that failed re-verification and degraded the instance to the free
	// tier — a security-relevant state transition that must not be
	// silent). Nil disables logging; mounting still succeeds.
	Logger *slog.Logger
}

// MountP10 attaches the four previously-orphaned handler packages.
// Each handler is safe to mount with partial deps; missing inputs
// surface as 404 / 503 at the route, not as a panic at boot.
func MountP10(r chi.Router, d P10Deps) {
	// Subscriptions. An explicitly-supplied store wins. Otherwise, if a
	// DB handle is available, back the store with the `licenses` table
	// (slot 0056) so an applied premium license survives a restart
	// (HLB-287). With no DB we fall back to the in-memory store — the
	// dev / no-Postgres path, where losing the license on restart is
	// acceptable.
	store := d.SubscriptionsStore
	if store == nil {
		if d.DB != nil {
			persist := subpkg.NewSQLLicensePersistence(d.DB)
			ps, err := subpkg.NewPersistentStore(context.Background(), persist, d.SubscriptionsVerifier)
			// On a recovery error the store is still usable (free
			// tier); a corrupt/unverifiable persisted license must not
			// crash boot nor silently grant premium. Fail-closed
			// behaviour is unchanged, but the silent premium->free
			// degradation is a security-relevant transition (tampered /
			// corrupt / key-rotated row), so emit a breadcrumb.
			if err != nil && d.Logger != nil {
				d.Logger.Warn("p10: persisted license unverifiable; degraded to free tier",
					"event", "license_recover_failed", "err", err)
			}
			store = ps
		} else {
			store = subpkg.NewStore()
		}
	}
	(&subh.Handler{Store: store, Verifier: d.SubscriptionsVerifier}).Mount(r)

	// Pairing. Prefer a persistent store: an in-memory store loses
	// every code on restart and is invisible to other replicas (the
	// audit's worst pairing gap). With a DB we back it with
	// `pairing_tickets` (slot 0055).
	pstore := d.PairingStore
	if pstore == nil {
		if d.DB != nil {
			pstore = discoverypkg.NewSQLPairingStore(d.DB)
		} else {
			pstore = discoverypkg.NewMemoryPairingStore()
		}
	}
	ttl := d.PairingTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	ph := &discoveryh.Handler{Store: pstore, TTL: ttl}
	// Exchange can only issue a working session when both a refresh
	// store (DB) and a signing key set are available. Without them the
	// handler returns 503 on Exchange instead of returning a body that
	// can't authenticate anything (the prior dead-end behaviour).
	if d.DB != nil && d.Keys != nil {
		uStore := users.New(d.DB)
		resolver := discoveryh.NewUsersAdminResolver(func(ctx context.Context, id string) (bool, error) {
			u, err := uStore.GetByID(ctx, id)
			if err != nil {
				return false, err
			}
			return u.IsAdmin, nil
		})
		ph.Minter = discoveryh.NewTokenMinter(resolver, refresh.New(d.DB), d.Keys)
		ph.Audit = pairAudit{w: securityaudit.NewWriter(d.DB)}
	}
	ph.Mount(r)

	// Security disclosure + SBOM.
	policy := d.SecurityPolicy
	if len(policy.Contact) == 0 {
		policy = securitypkg.DefaultPolicy()
	}
	(&securityh.Handler{Policy: policy, SBOM: d.SecuritySBOM}).Mount(r)

	// Perf admin.
	reg := d.PerfRegistry
	if reg == nil {
		reg = perfpkg.NewRegistry()
	}
	(&perfh.Handler{Registry: reg, Budgets: d.PerfBudgets}).Mount(r)
}

// pairAudit adapts a *securityaudit.Writer to the discovery handler's
// narrow AuditSink seam. A failed audit write must never fail the
// pairing request (best-effort, mirrors handlers/auth.audit).
type pairAudit struct{ w *securityaudit.Writer }

func (p pairAudit) WritePairEvent(ctx context.Context, claimed bool, actorUserID, code string) {
	if p.w == nil {
		return
	}
	ev := securityaudit.EventPairCodeIssued
	if claimed {
		ev = securityaudit.EventPairCodeClaimed
	}
	_ = p.w.Write(ctx, securityaudit.Entry{
		Event:       ev,
		ActorUserID: actorUserID,
		TargetID:    code,
		Payload:     map[string]any{"code": code},
	})
}
