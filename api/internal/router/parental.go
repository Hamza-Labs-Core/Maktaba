package router

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	discoveryh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/discovery"
	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
)

// MountParental mounts Epic 57 story 57.02's routes: restricted accounts, their
// guardians and their rules. With the bus's Postgres listener (busCtx and busDSN
// set) a policy is read from memory and dropped on slot 0221's parental_policy
// notification, so a tightened limit holds on the next request on every process;
// without it every request reads the database. It returns the cache the other
// surfaces filter with.
func MountParental(r chi.Router, db *sql.DB, loops *runner.Runner, busCtx context.Context, busDSN string, resettle func(context.Context) string) *parental.Cache {
	cache := parental.NewCache(parental.NewStore(db))
	if busCtx != nil && busDSN != "" {
		loops.Declare("parental_cache_listen", runner.Bound(busCtx, runner.Func(func(ctx context.Context) {
			cache.Listen(ctx, busDSN, resettle, slog.Default())
		})))
	}
	(&parentalh.Handler{Store: cache.Store(), Cache: cache}).Mount(r)
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
