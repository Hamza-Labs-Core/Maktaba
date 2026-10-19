package streaming

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg/runtimecfgtest"
)

// session.url_ttl_sec sets a signed stream URL's lifetime (a video longer than
// it still gets its length plus an hour); unset: SessionTTL, else 30 min.
func TestSessionURLTTLSetting(t *testing.T) {
	ctx := context.Background()
	db := runtimecfgtest.DB(t)
	h := &Handler{Settings: runtimecfg.New(db)}
	short := sql.NullFloat64{} // length unknown
	if got := h.urlTTL(ctx, short); got != 30*time.Minute {
		t.Fatalf("default %v", got)
	}
	h.SessionTTL = 10 * time.Minute
	if got := h.urlTTL(ctx, short); got != 10*time.Minute {
		t.Fatalf("SessionTTL %v", got)
	}
	runtimecfgtest.Set(t, db, "session.url_ttl_sec", `7200`)
	h.Settings.Invalidate()
	if got := h.urlTTL(ctx, short); got != 2*time.Hour {
		t.Fatalf("session.url_ttl_sec 7200: %v", got)
	}
	long := sql.NullFloat64{Float64: 3 * 3600, Valid: true}
	if got := h.urlTTL(ctx, long); got != 4*time.Hour {
		t.Fatalf("a 3 h video: %v", got)
	}
}
