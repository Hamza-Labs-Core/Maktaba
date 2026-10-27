package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// analyticsSaltName names the home's salt for the viewers' addresses in analytics.
const analyticsSaltName = "analytics.ip_salt"

// AnalyticsSalt is the home's secret salt for the hashed viewer addresses analytics
// keeps (watch_sessions.ip_addr_hash): random, made once and sealed in home_secrets.
// An address hashed without a secret salt is no secret (every IPv4 address can be
// tried), so with no secrets store or key the answer is "" and no hash is kept.
func AnalyticsSalt(ctx context.Context, store secrets.Store, logger *slog.Logger) string {
	if store == nil {
		return ""
	}
	b, err := store.Get(ctx, analyticsSaltName)
	if errors.Is(err, secrets.ErrNotFound) {
		b = make([]byte, 32)
		if _, err = rand.Read(b); err == nil {
			err = store.Put(ctx, analyticsSaltName, b, "")
		}
	}
	if err != nil {
		if logger != nil {
			logger.Warn("analytics: no salt for viewer addresses; none are kept",
				"event", "analytics_salt_unavailable", "err", err)
		}
		return ""
	}
	return hex.EncodeToString(b)
}
