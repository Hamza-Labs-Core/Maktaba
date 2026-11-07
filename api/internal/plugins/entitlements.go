package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/storelink"
)

// Entitlements of paid plugins (Epic 54, story 54.19 §5, §6). A home linked to a store
// account asks the store for its entitlements with its own signature (GET
// /v1/homes/{id}/entitlements), daily, and every 15 s for 15 minutes after a Buy it started;
// the supervisor checks each offline against the store keys it verified (shared/plugins/
// pluginsig, CheckEntitlement); the ones that hold replace plugin_entitlements. The gates (the
// supervisor's runner, the pipeline's plugingate) then read only valid_until: a paid plugin
// with none valid now, with the 72-hour grace, waits entitlement_missing. A store, cloud or
// network outage keeps the rows: a paying home runs on for 31 days without the store, and
// for ever without Maktaba Cloud, which nothing here calls.

// Refresh intervals (54.19 §5).
const (
	EntitlementsEvery    = 24 * time.Hour
	EntitlementsBurst    = 15 * time.Second
	EntitlementsBurstFor = 15 * time.Minute
)

// EntitlementSource is the home's store link (storelink.Link).
type EntitlementSource interface {
	LoadState(ctx context.Context) (*storelink.State, error)
	KeyFingerprint(ctx context.Context) (string, error)
	Signed(ctx context.Context, method, path string, body []byte) (*http.Response, []byte, error)
}

// EntitlementsCheck is what the supervisor checks (its POST /plugins/entitlements/verify).
type EntitlementsCheck struct {
	HomeID       string            `json:"home_id"`
	HomeKeyFP    string            `json:"home_key_fp"`
	Entitlements []json.RawMessage `json:"entitlements"`
}

// CheckedEntitlement is one that holds, as the supervisor answers it.
type CheckedEntitlement struct {
	PluginID   string          `json:"plugin_id"`
	PurchaseID string          `json:"purchase_id"`
	Product    string          `json:"product"`
	ValidFrom  time.Time       `json:"valid_from"`
	ValidUntil time.Time       `json:"valid_until"`
	IssuedAt   time.Time       `json:"issued_at"`
	Envelope   json.RawMessage `json:"envelope"`
}

// RefusedEntitlement is one that does not, by its place in the store's answer.
type RefusedEntitlement struct {
	Index int    `json:"index"`
	Code  string `json:"code"`
	Step  string `json:"step"`
}

// EntitlementsChecked is the supervisor's answer.
type EntitlementsChecked struct {
	Valid   []CheckedEntitlement `json:"valid"`
	Refused []RefusedEntitlement `json:"refused"`
}

// EntitlementVerifier checks entitlements offline (the supervisor, SupervisorHTTP).
type EntitlementVerifier interface {
	VerifyEntitlements(ctx context.Context, c EntitlementsCheck) (*EntitlementsChecked, error)
}

// Entitlement codes the routes answer (words in web/src/i18n as plugins.store.error.<code>).
const (
	CodeEntitlementsNotLinked = "store_not_linked"
	CodeEntitlementsFailed    = "entitlements_unavailable"
)

// RefreshResult is what one refresh did.
type RefreshResult struct {
	Linked  bool                 `json:"linked"`
	Valid   int                  `json:"valid"`
	Refused []RefusedEntitlement `json:"refused"`
	At      time.Time            `json:"at"`
}

// Entitlements keeps plugin_entitlements following the store.
type Entitlements struct {
	DB       *sql.DB
	Link     EntitlementSource
	Verifier EntitlementVerifier
	Logger   *slog.Logger
	Now      func() time.Time

	mu         sync.Mutex
	burstUntil time.Time
	wake       chan struct{}
	last       *RefreshResult
}

func (e *Entitlements) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Entitlements) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// Refresh asks the store once and replaces the rows with the entitlements that hold. A home
// not linked changes nothing (its rows lapse at their valid_until); any failure on the way
// keeps the rows as they are.
func (e *Entitlements) Refresh(ctx context.Context) (*RefreshResult, error) {
	st, err := e.Link.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	res := &RefreshResult{At: e.now().UTC(), Refused: []RefusedEntitlement{}}
	if st == nil {
		return res, nil
	}
	res.Linked = true
	fp, err := e.Link.KeyFingerprint(ctx)
	if err != nil {
		return nil, err
	}
	resp, body, err := e.Link.Signed(ctx, http.MethodGet, "/v1/homes/"+st.HomeID+"/entitlements", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: the store answered %d", CodeEntitlementsFailed, resp.StatusCode)
	}
	var doc struct {
		HomeID       string            `json:"home_id"`
		Entitlements []json.RawMessage `json:"entitlements"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.HomeID != st.HomeID {
		return nil, fmt.Errorf("%s: not this home's entitlements", CodeEntitlementsFailed)
	}
	checked, err := e.Verifier.VerifyEntitlements(ctx, EntitlementsCheck{HomeID: st.HomeID, HomeKeyFP: fp, Entitlements: doc.Entitlements})
	if err != nil {
		return nil, err
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// the store's answer is the whole set: a refunded or lost purchase is gone from it
	if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_entitlements`); err != nil {
		return nil, err
	}
	for _, c := range checked.Valid {
		if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_entitlements (purchase_id, plugin_id, product, store_home,
			valid_from, valid_until, issued_at, envelope, refreshed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (purchase_id) DO UPDATE SET valid_until = GREATEST(plugin_entitlements.valid_until, EXCLUDED.valid_until)`,
			c.PurchaseID, c.PluginID, c.Product, st.HomeID, c.ValidFrom, c.ValidUntil, c.IssuedAt, string(c.Envelope), res.At); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	res.Valid = len(checked.Valid)
	res.Refused = checked.Refused
	for _, r := range checked.Refused {
		e.logger().WarnContext(ctx, "plugins: an entitlement did not hold", "event", "plugin_entitlement_refused",
			"code", r.Code, "step", r.Step)
	}
	e.mu.Lock()
	e.last = res
	e.mu.Unlock()
	return res, nil
}

// Burst asks every 15 s for the next 15 minutes (after a Buy this home started).
func (e *Entitlements) Burst() {
	e.mu.Lock()
	e.burstUntil = e.now().Add(EntitlementsBurstFor)
	w := e.wake
	e.mu.Unlock()
	if w != nil {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// Last is the last refresh that ran (nil: none since the API started).
func (e *Entitlements) Last() *RefreshResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.last
}

// next is how long until the next refresh: 15 s during a burst, else a day (with up to an
// hour of jitter, so linked homes do not ask the store at the same second).
func (e *Entitlements) next() time.Duration {
	e.mu.Lock()
	burst := e.now().Before(e.burstUntil)
	e.mu.Unlock()
	if burst {
		return EntitlementsBurst
	}
	return EntitlementsEvery + time.Duration(rand.Int64N(int64(time.Hour)))
}

// Run refreshes a minute after the start, then on its schedule, until ctx ends.
func (e *Entitlements) Run(ctx context.Context) {
	e.mu.Lock()
	e.wake = make(chan struct{}, 1)
	w := e.wake
	e.mu.Unlock()
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-w:
		}
		if _, err := e.Refresh(ctx); err != nil && ctx.Err() == nil {
			var se *storelink.Error
			if !errors.As(err, &se) || se.Code != storelink.CodeOffline {
				e.logger().WarnContext(ctx, "plugins: the entitlements were not refreshed", "event", "plugin_entitlements_failed",
					"err", err)
			}
		}
		wait = e.next()
	}
}

// List is the entitlements this home holds, for the admin page.
func (e *Entitlements) List(ctx context.Context) ([]map[string]any, error) {
	rows, err := e.DB.QueryContext(ctx, `SELECT plugin_id, product, purchase_id, valid_until, refreshed_at
		FROM plugin_entitlements ORDER BY plugin_id, purchase_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var pid, product, purchase string
		var until, refreshed time.Time
		if err := rows.Scan(&pid, &product, &purchase, &until, &refreshed); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"plugin_id": pid, "product": product, "purchase_id": purchase,
			"valid_until": until.UTC(), "refreshed_at": refreshed.UTC()})
	}
	return out, rows.Err()
}

// VerifyEntitlements asks this home's supervisor to check entitlements offline.
func (s *SupervisorHTTP) VerifyEntitlements(ctx context.Context, c EntitlementsCheck) (*EntitlementsChecked, error) {
	b, _ := json.Marshal(c)
	resp, err := s.Sup.Do(ctx, http.MethodPost, "/plugins/entitlements/verify", strings.NewReader(string(b)), "application/json")
	if err != nil {
		return nil, fmt.Errorf("%s: %v", CodeNoSupervisor, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: the supervisor answered %d: %s", CodeEntitlementsFailed, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out EntitlementsChecked
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
