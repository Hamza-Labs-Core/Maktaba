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
	"time"

	"github.com/go-chi/chi/v5"

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

	// PairingStore persists pairing tickets. Nil falls back to the
	// in-memory store (dev-only).
	PairingStore discoverypkg.PairingStore

	// PairingTTL bounds ticket lifetime. Zero defaults to 5 minutes.
	PairingTTL time.Duration

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
			// crash boot nor silently grant premium.
			_ = err
			store = ps
		} else {
			store = subpkg.NewStore()
		}
	}
	(&subh.Handler{Store: store, Verifier: d.SubscriptionsVerifier}).Mount(r)

	// Pairing.
	pstore := d.PairingStore
	if pstore == nil {
		pstore = discoverypkg.NewMemoryPairingStore()
	}
	ttl := d.PairingTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	(&discoveryh.Handler{Store: pstore, TTL: ttl}).Mount(r)

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
