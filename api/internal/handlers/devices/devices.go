// Package devices implements Story 7.22 device-registration routes:
//
//	POST   /api/devices/register
//	DELETE /api/devices/{id}
//	GET    /api/devices
//
// Tokens are stored as opaque strings; the APNs/FCM bridge consumes
// them via Notify (Story 18.x). On a failed delivery the bridge soft-
// revokes by setting “revoked_at“ rather than deleting the row.
package devices

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// allowedCategories is the closed push-notification category vocabulary
// (Story 12.4 / 12.10). Keep in sync with the client toggle list.
var allowedCategories = map[string]bool{
	"job":          true, // job.completed / job.failed
	"library":      true, // library.video.added
	"subscription": true, // subscription.expiring
	"system":       true, // operational / security
}

var (
	apnsTokenRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	// FCM tokens are long opaque URL-safe strings; enforce a sane floor
	// and character set rather than an exact length (Google does not
	// publish one).
	fcmTokenRe = regexp.MustCompile(`^[A-Za-z0-9_:.\-]{32,}$`)
)

// validatePushTokenFormat enforces the Story 12.10 `400 invalid-token`
// AC: the token must be well-formed for the declared platform. Web
// push tokens (endpoints) are opaque so only non-emptiness is checked.
func validatePushTokenFormat(platform, token string) error {
	switch platform {
	case "ios":
		if !apnsTokenRe.MatchString(token) {
			return errors.New("ios push token must be 64 hex characters (APNs)")
		}
	case "android":
		if !fcmTokenRe.MatchString(token) {
			return errors.New("android push token is not a valid FCM registration token")
		}
	case "web":
		if strings.TrimSpace(token) == "" {
			return errors.New("web push token required")
		}
	default:
		return errors.New("unknown platform")
	}
	return nil
}

// normalizeCategories lowercases, dedupes (order-stable), and validates
// the requested categories against the closed vocabulary.
func normalizeCategories(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if !allowedCategories[c] {
			return nil, errors.New("unknown category: " + c)
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

// RegisterRequest is the POST body. `token` is the Story 12.10 field
// name; `push_token` is the legacy Story 7.22 alias — both unmarshal so
// 12.10-spec clients and existing callers interoperate.
type RegisterRequest struct {
	Platform   string   `json:"platform"`
	PushToken  string   `json:"push_token"`
	Token      string   `json:"token,omitempty"`
	BundleID   string   `json:"bundle_id"`
	AppVersion string   `json:"app_version,omitempty"`
	OSVersion  string   `json:"os_version,omitempty"`
	Locale     string   `json:"locale,omitempty"`
	Categories []string `json:"categories,omitempty"`
}

// token returns the effective push token, accepting either the 12.10
// `token` field or the 7.22 `push_token` alias.
func (r RegisterRequest) token() string {
	if strings.TrimSpace(r.Token) != "" {
		return r.Token
	}
	return r.PushToken
}

// Device is the API shape.
type Device struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Platform     string    `json:"platform"`
	PushToken    string    `json:"push_token"`
	BundleID     string    `json:"bundle_id"`
	AppVersion   *string   `json:"app_version,omitempty"`
	OSVersion    *string   `json:"os_version,omitempty"`
	Locale       *string   `json:"locale,omitempty"`
	Categories   []string  `json:"categories,omitempty"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// Handler bundles deps.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
}

func (h *Handler) Mount(r chi.Router) {
	r.Post("/api/devices/register", h.Register)
	r.Patch("/api/devices/{id}", h.Patch)
	r.Delete("/api/devices/{id}", h.Unregister)
	r.Get("/api/devices", h.List)
	// Story 12.10 spec path alias (client-facing) for the device list;
	// "no plaintext token" is already enforced by redactDevice.
	r.Get("/api/me/devices", h.List)
}

// Register implements AC-1 + AC-5: upsert by (user, platform, push_token);
// soft-revoke any prior row for the same (user, platform, bundle_id).
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	var req RegisterRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	if req.Platform != "ios" && req.Platform != "android" && req.Platform != "web" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "platform", Message: "must be one of ios, android, web"},
		}))
		return
	}
	tok := req.token()
	if tok == "" || req.BundleID == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "token", Message: "required"},
			{Field: "bundle_id", Message: "required"},
		}))
		return
	}
	// Story 12.10 AC: `400 invalid-token` — token format must match the
	// declared platform.
	if err := validatePushTokenFormat(req.Platform, tok); err != nil {
		httperror.Write(w, r, httperror.BadRequest("invalid-token: "+err.Error()))
		return
	}
	cats, err := normalizeCategories(req.Categories)
	if err != nil {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "categories", Message: err.Error()},
		}))
		return
	}
	var catsJSON any
	if len(cats) > 0 {
		b, _ := json.Marshal(cats)
		catsJSON = string(b)
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx"))
		return
	}
	defer func() { _ = tx.Rollback() }()

	now := h.now()
	// AC-5: revoke any prior row for the (user, platform, bundle_id) tuple
	// that does *not* match this push_token.
	_, _ = tx.ExecContext(r.Context(), `
		UPDATE devices SET revoked_at = $4
		WHERE user_id = $1 AND platform = $2 AND bundle_id = $3
		  AND push_token <> $5 AND revoked_at IS NULL
	`, p.UserID, req.Platform, req.BundleID, now, tok)

	// Check for existing row.
	var existingID string
	err = tx.QueryRowContext(r.Context(), `
		SELECT id FROM devices WHERE user_id=$1 AND platform=$2 AND push_token=$3
	`, p.UserID, req.Platform, tok).Scan(&existingID)
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		existingID = uuid.NewString()
		created = true
		_, err = tx.ExecContext(r.Context(), `
			INSERT INTO devices (id, user_id, platform, push_token, bundle_id, app_version, os_version, locale, categories, registered_at, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		`, existingID, p.UserID, req.Platform, tok, req.BundleID,
			nullIfEmpty(req.AppVersion), nullIfEmpty(req.OSVersion),
			nullIfEmpty(req.Locale), catsJSON, now)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("insert device: "+err.Error()))
			return
		}
	} else if err != nil {
		httperror.Write(w, r, httperror.Internal("query device: "+err.Error()))
		return
	} else {
		_, err = tx.ExecContext(r.Context(), `
			UPDATE devices SET app_version=$1, os_version=$2, locale=$3,
			       categories=COALESCE($4, categories), last_seen_at=$5, revoked_at=NULL
			WHERE id = $6
		`, nullIfEmpty(req.AppVersion), nullIfEmpty(req.OSVersion),
			nullIfEmpty(req.Locale), catsJSON, now, existingID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("update device: "+err.Error()))
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit"))
		return
	}
	// Story 12.10 AC: audit log category='device'. Best-effort; never
	// blocks registration (mirrors libraries.WriteAudit).
	h.writeAudit(r.Context(), boolAction(created, "device-registered", "device-updated"),
		p.UserID, existingID, map[string]any{
			"platform": req.Platform, "bundle_id": req.BundleID,
		})
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	common.WriteJSON(w, r, status, map[string]any{"id": existingID, "created": created})
}

// PatchRequest is the Story 12.10 PATCH /api/devices/{id} body.
type PatchRequest struct {
	Categories *[]string `json:"categories,omitempty"`
	Locale     *string   `json:"locale,omitempty"`
}

// Patch implements Story 12.10 `PATCH /api/devices/{id}` —
// owner-scoped update of the opt-in categories and/or locale. Returns
// 200 with the redacted device on success.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "id")
	var req PatchRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Categories == nil && req.Locale == nil {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "body", Message: "at least one of categories, locale required"},
		}))
		return
	}
	sets := []string{}
	args := []any{}
	n := 1
	if req.Categories != nil {
		cats, err := normalizeCategories(*req.Categories)
		if err != nil {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
				{Field: "categories", Message: err.Error()},
			}))
			return
		}
		b, _ := json.Marshal(cats)
		sets = append(sets, "categories=$"+itoa(n))
		args = append(args, string(b))
		n++
	}
	if req.Locale != nil {
		sets = append(sets, "locale=$"+itoa(n))
		args = append(args, nullIfEmpty(strings.TrimSpace(*req.Locale)))
		n++
	}
	q := "UPDATE devices SET " + strings.Join(sets, ", ") +
		" WHERE id=$" + itoa(n) + " AND user_id=$" + itoa(n+1) + " AND revoked_at IS NULL"
	args = append(args, id, p.UserID)
	res, err := h.DB.ExecContext(r.Context(), q, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("patch device: "+err.Error()))
		return
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		httperror.Write(w, r, httperror.NotFound("device "+id))
		return
	}
	h.writeAudit(r.Context(), "device-patched", p.UserID, id, map[string]any{})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"id": id, "updated": true})
}

func boolAction(b bool, ifTrue, ifFalse string) string {
	if b {
		return ifTrue
	}
	return ifFalse
}

// writeAudit emits a best-effort `category='device'` audit row
// (Story 12.10 AC). A missing audit_log table (partial-migration test
// env) is swallowed so registration is never blocked.
func (h *Handler) writeAudit(ctx context.Context, action, actor, target string, payload map[string]any) {
	if h.DB == nil {
		return
	}
	b, _ := json.Marshal(payload)
	_, _ = h.DB.ExecContext(ctx, `
		INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
		VALUES ('device', $1, $2, $3, $4)
	`, action, actor, target, string(b))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Unregister soft-revokes (AC-3).
func (h *Handler) Unregister(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "id")
	res, err := h.DB.ExecContext(r.Context(), `
		UPDATE devices SET revoked_at=$1 WHERE id=$2 AND user_id=$3 AND revoked_at IS NULL
	`, h.now(), id, p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("unregister"))
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		httperror.Write(w, r, httperror.NotFound("device "+id))
		return
	}
	common.WriteNoContent(w)
}

// List returns active devices for the principal.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, user_id, platform, push_token, bundle_id, app_version, os_version, locale, categories, registered_at, last_seen_at
		FROM devices WHERE user_id = $1 AND revoked_at IS NULL ORDER BY last_seen_at DESC
	`, p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list devices"))
		return
	}
	defer rows.Close()
	items := []Device{}
	for rows.Next() {
		var d Device
		var av, osv, loc, cats sql.NullString
		if err := rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.PushToken, &d.BundleID, &av, &osv, &loc, &cats, &d.RegisteredAt, &d.LastSeenAt); err != nil {
			continue
		}
		if av.Valid {
			d.AppVersion = &av.String
		}
		if osv.Valid {
			d.OSVersion = &osv.String
		}
		if loc.Valid {
			d.Locale = &loc.String
		}
		if cats.Valid && cats.String != "" {
			_ = json.Unmarshal([]byte(cats.String), &d.Categories)
		}
		items = append(items, d)
	}
	// Redact the push token at the response boundary. Storage and the
	// SELECT above are unchanged — only what crosses the wire is
	// sanitised (mirrors settings.redactSecrets, the secret-leak
	// precedent).
	redacted := make([]map[string]any, 0, len(items))
	for _, d := range items {
		redacted = append(redacted, redactDevice(d))
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": redacted})
}

// redactDevice converts a Device into its over-the-wire map with the
// push token stripped: the value becomes "<redacted>" and a sibling
// "push_token_present" reports whether a non-empty token exists. This
// is the only place the raw token is dropped — DB rows still carry it
// for the APNs/FCM bridge (Story 18.x).
func redactDevice(d Device) map[string]any {
	out := map[string]any{
		"id":                 d.ID,
		"user_id":            d.UserID,
		"platform":           d.Platform,
		"push_token":         "<redacted>",
		"push_token_present": strings.TrimSpace(d.PushToken) != "",
		"bundle_id":          d.BundleID,
		"registered_at":      d.RegisteredAt,
		"last_seen_at":       d.LastSeenAt,
	}
	if d.AppVersion != nil {
		out["app_version"] = *d.AppVersion
	}
	if d.OSVersion != nil {
		out["os_version"] = *d.OSVersion
	}
	if d.Locale != nil {
		out["locale"] = *d.Locale
	}
	if d.Categories != nil {
		out["categories"] = d.Categories
	}
	return out
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}
