// Package flags is the HTTP surface of feature-flag resolution (Epic 16
// Story 16.8; the client side is Story 16.6):
//
//	GET    /api/me/flags                               any signed-in user: the signed bundle
//	GET    /api/me/flags/keys                          any signed-in user: the verification keys
//	GET    /api/me/cohorts                             any signed-in user: the cohorts one can join
//	POST   /api/me/cohorts {cohort}                    join (user_opt_in cohorts only)
//	DELETE /api/me/cohorts/{cohort}                    leave (user_opt_in cohorts only)
//	GET    /api/admin/flags[?user_id=]                 admin: declarations, overrides, cohorts, one user's resolution
//	POST   /api/admin/flags/overrides                  admin
//	PATCH  /api/admin/flags/overrides/{id}             admin
//	DELETE /api/admin/flags/overrides/{id}             admin
//	POST   /api/admin/cohorts/{cohort}/users {user_ids} admin, at most 1000 ids (413 above)
//	DELETE /api/admin/cohorts/{cohort}/users/{user_id} admin
//
// Every role rule is checked here, on the server. A Maktaba Cloud
// sign-in lowered to play scope is no admin (principal.ApplyCloudScope
// clears IsAdmin), so the admin routes refuse it. Every admin write is
// audited (audit_log, category flags).
package flags

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/serverkeys"
	flagspkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/flags"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// MaxBatch is the most user ids one cohort add takes (Story 16.8 EC).
const MaxBatch = 1000

// Handler serves the routes above.
type Handler struct {
	DB       *sql.DB
	Resolver *flagspkg.Resolver
	Logger   *slog.Logger
}

// New builds the handler over db, the license view ent and the identity
// keys used to sign.
func New(db *sql.DB, ent flagspkg.Entitlements, keys *serverkeys.Store, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		DB:       db,
		Resolver: &flagspkg.Resolver{Store: &flagspkg.Store{DB: db}, Entitlements: ent, Keys: keys},
		Logger:   logger,
	}
}

// Mount registers the routes on r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/me/flags", h.meFlags)
	r.Get("/api/me/flags/keys", h.meKeys)
	r.Get("/api/me/cohorts", h.meCohorts)
	r.Post("/api/me/cohorts", h.joinCohort)
	r.Delete("/api/me/cohorts/{cohort}", h.leaveCohort)
	r.Get("/api/admin/flags", h.adminView)
	r.Post("/api/admin/flags/overrides", h.createOverride)
	r.Patch("/api/admin/flags/overrides/{id}", h.patchOverride)
	r.Delete("/api/admin/flags/overrides/{id}", h.deleteOverride)
	r.Post("/api/admin/cohorts/{cohort}/users", h.addToCohort)
	r.Delete("/api/admin/cohorts/{cohort}/users/{user_id}", h.removeFromCohort)
}

func (h *Handler) store() *flagspkg.Store { return h.Resolver.Store }

func unauthorized() *httperror.Error {
	return &httperror.Error{Type: httperror.TypeForbidden, Title: "unauthorized",
		Status: http.StatusUnauthorized, Detail: "authentication required"}
}

// signedIn returns the caller, or writes 401 and returns nil.
func signedIn(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return nil
	}
	return p
}

// signedInUser is signedIn for routes about the caller's own account: a
// principal that is no user (the admin token) has no cohorts.
func signedInUser(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := signedIn(w, r)
	if p == nil {
		return nil
	}
	if _, err := uuid.Parse(p.UserID); err != nil {
		httperror.Write(w, r, httperror.Forbidden("", "this credential is no user account"))
		return nil
	}
	return p
}

// admin returns the caller when an admin, or writes 401/403.
func admin(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := signedIn(w, r)
	if p == nil {
		return nil
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return nil
	}
	return p
}

func (h *Handler) available(w http.ResponseWriter, r *http.Request) bool {
	if h.DB == nil || h.Resolver == nil || h.Resolver.Keys == nil {
		w.Header().Set("Retry-After", "60")
		httperror.Write(w, r, httperror.Unavailable(60))
		return false
	}
	return true
}

func subject(p *principal.Principal) flagspkg.Subject {
	return flagspkg.Subject{UserID: p.UserID, IsAdmin: p.IsAdmin}
}

func (h *Handler) meFlags(w http.ResponseWriter, r *http.Request) {
	p := signedIn(w, r)
	if p == nil || !h.available(w, r) {
		return
	}
	b, err := h.Resolver.Bundle(r.Context(), subject(p))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("resolve flags").Wrap(err))
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	common.WriteJSON(w, r, http.StatusOK, b)
}

func (h *Handler) meKeys(w http.ResponseWriter, r *http.Request) {
	if signedIn(w, r) == nil || !h.available(w, r) {
		return
	}
	common.WriteJSON(w, r, http.StatusOK, h.Resolver.Keys.JWKS())
}

type cohortView struct {
	flagspkg.Cohort
	Joined bool `json:"joined"`
}

func (h *Handler) meCohorts(w http.ResponseWriter, r *http.Request) {
	p := signedInUser(w, r)
	if p == nil || !h.available(w, r) {
		return
	}
	mine, err := h.store().CohortsOf(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read cohorts").Wrap(err))
		return
	}
	in := map[string]bool{}
	for _, c := range mine {
		in[c] = true
	}
	out := []cohortView{}
	for _, c := range flagspkg.Cohorts {
		if c.UserOptIn {
			out = append(out, cohortView{Cohort: c, Joined: in[c.Name]})
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// optInCohort returns the declared user_opt_in cohort name, or writes
// 403 (a cohort only an admin assigns, or none).
func optInCohort(w http.ResponseWriter, r *http.Request, name string) bool {
	if c, ok := flagspkg.LookupCohort(name); !ok || !c.UserOptIn {
		httperror.Write(w, r, httperror.Forbidden("", "this cohort cannot be joined or left by its members"))
		return false
	}
	return true
}

func (h *Handler) joinCohort(w http.ResponseWriter, r *http.Request) {
	p := signedInUser(w, r)
	if p == nil || !h.available(w, r) {
		return
	}
	var body struct {
		Cohort string `json:"cohort"`
	}
	if e := common.ReadJSON(r, &body, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !optInCohort(w, r, body.Cohort) {
		return
	}
	if _, err := h.store().AddToCohort(r.Context(), body.Cohort, []string{p.UserID}); err != nil {
		httperror.Write(w, r, httperror.Internal("join cohort").Wrap(err))
		return
	}
	h.Resolver.Invalidate()
	common.WriteNoContent(w)
}

func (h *Handler) leaveCohort(w http.ResponseWriter, r *http.Request) {
	p := signedInUser(w, r)
	if p == nil || !h.available(w, r) {
		return
	}
	name := chi.URLParam(r, "cohort")
	if !optInCohort(w, r, name) {
		return
	}
	if err := h.store().RemoveFromCohort(r.Context(), name, p.UserID); err != nil && !errors.Is(err, flagspkg.ErrNotFound) {
		httperror.Write(w, r, httperror.Internal("leave cohort").Wrap(err))
		return
	}
	h.Resolver.Invalidate()
	common.WriteNoContent(w)
}

// overrideView is an override as the admin page shows it.
type overrideView struct {
	flagspkg.Override
	// Unknown: the binary declares no such flag; the bundle leaves it out.
	Unknown bool `json:"unknown,omitempty"`
	// Expired: past expires_at, no longer applied.
	Expired bool `json:"expired,omitempty"`
	// Stale: a newer override of the same flag, scope and target wins.
	Stale bool `json:"stale,omitempty"`
}

func views(rows []flagspkg.Override, now time.Time) []overrideView {
	out := make([]overrideView, len(rows))
	winner := map[[3]string]int{}
	for i, o := range rows {
		_, known := flagspkg.Lookup(o.FlagKey)
		out[i] = overrideView{Override: o, Unknown: !known,
			Expired: o.ExpiresAt != nil && !now.Before(*o.ExpiresAt)}
		if out[i].Expired {
			continue
		}
		k := [3]string{o.FlagKey, o.Scope, o.ScopeValue}
		if j, seen := winner[k]; seen {
			out[j].Stale = true // rows are oldest first: this one replaces it
		}
		winner[k] = i
	}
	return out
}

func (h *Handler) adminView(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil || !h.available(w, r) {
		return
	}
	ctx := r.Context()
	rows, err := h.store().List(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list overrides").Wrap(err))
		return
	}
	sizes, err := h.store().CohortSizes(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("cohort sizes").Wrap(err))
		return
	}
	type cohortRow struct {
		Name        string `json:"name"`
		UserOptIn   bool   `json:"user_opt_in"`
		Declared    bool   `json:"declared"`
		Description string `json:"description,omitempty"`
		Members     int    `json:"members"`
	}
	cohorts := []cohortRow{}
	for _, c := range flagspkg.Cohorts {
		cohorts = append(cohorts, cohortRow{Name: c.Name, UserOptIn: c.UserOptIn, Declared: true,
			Description: c.Description, Members: sizes[c.Name]})
		delete(sizes, c.Name)
	}
	for name, n := range sizes {
		cohorts = append(cohorts, cohortRow{Name: name, Members: n})
	}
	lic := h.Resolver.Entitlements.Current()
	tier := "free"
	if lic != nil && lic.Tier != "" {
		tier = string(lic.Tier)
	}
	out := map[string]any{
		"declarations": flagspkg.Declarations,
		"overrides":    views(rows, time.Now()),
		"cohorts":      cohorts,
		"tier":         tier,
	}
	if uid := r.URL.Query().Get("user_id"); uid != "" {
		if _, err := uuid.Parse(uid); err != nil {
			httperror.Write(w, r, httperror.InvalidQuery("user_id must be a UUID"))
			return
		}
		found, isAdmin, err := h.store().UserIsAdmin(ctx, uid)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("read user").Wrap(err))
			return
		}
		if !found {
			httperror.Write(w, r, httperror.NotFound("user"))
			return
		}
		values, err := h.Resolver.Resolve(ctx, flagspkg.Subject{UserID: uid, IsAdmin: isAdmin}, lic)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("resolve flags").Wrap(err))
			return
		}
		out["resolved"] = map[string]any{"user_id": uid, "flags": values}
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// scalar reports whether v (decoded JSON) is a flag value: a boolean,
// number or string.
func scalar(v any) bool {
	switch v.(type) {
	case bool, float64, string:
		return true
	}
	return false
}

func fieldErr(field, msg string) *httperror.Error {
	return httperror.Unprocessable([]httperror.FieldError{{Field: field, Message: msg}})
}

// checkValue validates an override's value for key: a declared flag
// takes a boolean, an undeclared one any scalar.
func checkValue(key string, v any) *httperror.Error {
	if _, known := flagspkg.Lookup(key); known {
		if _, ok := v.(bool); !ok {
			return fieldErr("value", "must be true or false")
		}
		return nil
	}
	if !scalar(v) {
		return fieldErr("value", "must be a boolean, number or string")
	}
	return nil
}

func checkExpiry(t *time.Time) *httperror.Error {
	if t != nil && !t.After(time.Now()) {
		return fieldErr("expires_at", "must be in the future")
	}
	return nil
}

func (h *Handler) createOverride(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil || !h.available(w, r) {
		return
	}
	var body struct {
		FlagKey    string     `json:"flag_key"`
		Scope      string     `json:"scope"`
		ScopeValue string     `json:"scope_value"`
		Value      any        `json:"value"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}
	if e := common.ReadJSON(r, &body, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !flagspkg.ValidKey(body.FlagKey) {
		httperror.Write(w, r, fieldErr("flag_key", "lowercase letters, digits, '_', '.', '-' (at most 64)"))
		return
	}
	if e := checkValue(body.FlagKey, body.Value); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if e := checkExpiry(body.ExpiresAt); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if e := h.checkScope(r.Context(), body.Scope, body.ScopeValue); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if body.Scope == flagspkg.ScopeUser {
		body.ScopeValue = uuid.MustParse(body.ScopeValue).String() // the form the resolver matches
	}
	o, err := h.store().Create(r.Context(), flagspkg.Override{FlagKey: body.FlagKey, Scope: body.Scope,
		ScopeValue: body.ScopeValue, Value: body.Value, ExpiresAt: body.ExpiresAt, CreatedBy: p.UserID})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("create override").Wrap(err))
		return
	}
	h.Resolver.Invalidate()
	h.audit(r.Context(), "flags.override.created", p.UserID, o.ID, map[string]any{
		"flag_key": o.FlagKey, "scope": o.Scope, "scope_value": o.ScopeValue, "value": o.Value, "expires_at": o.ExpiresAt})
	common.WriteJSON(w, r, http.StatusCreated, views([]flagspkg.Override{o}, time.Now())[0])
}

// checkScope validates an override's target.
func (h *Handler) checkScope(ctx context.Context, scope, value string) *httperror.Error {
	switch scope {
	case flagspkg.ScopeGlobal:
		if value != "" {
			return fieldErr("scope_value", "must be empty for a global override")
		}
	case flagspkg.ScopeTier:
		if !flagspkg.ValidTier(value) {
			return fieldErr("scope_value", "must be free, home or pro")
		}
	case flagspkg.ScopeCohort:
		if !flagspkg.ValidCohort(value) {
			return fieldErr("scope_value", "a cohort name: lowercase letters, digits, '_', '-' (at most 64)")
		}
	case flagspkg.ScopeUser:
		if _, err := uuid.Parse(value); err != nil {
			return fieldErr("scope_value", "must be a user id")
		}
		found, _, err := h.store().UserIsAdmin(ctx, value)
		if err != nil {
			return httperror.Internal("read user").Wrap(err)
		}
		if !found {
			return fieldErr("scope_value", "no such user")
		}
	default:
		return fieldErr("scope", "must be global, tier, cohort or user")
	}
	return nil
}

func (h *Handler) patchOverride(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil || !h.available(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("override"))
		return
	}
	// expires_at: absent keeps it, null clears it
	var body struct {
		Value     *json.RawMessage `json:"value"`
		ExpiresAt *json.RawMessage `json:"expires_at"`
	}
	if e := common.ReadJSON(r, &body, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	o, err := h.store().Get(ctx, id)
	if errors.Is(err, flagspkg.ErrNotFound) {
		httperror.Write(w, r, httperror.NotFound("override"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read override").Wrap(err))
		return
	}
	before := o
	if body.Value != nil {
		var v any
		if err := json.Unmarshal(*body.Value, &v); err != nil {
			httperror.Write(w, r, fieldErr("value", "invalid"))
			return
		}
		if e := checkValue(o.FlagKey, v); e != nil {
			httperror.Write(w, r, e)
			return
		}
		o.Value = v
	}
	if body.ExpiresAt != nil {
		var t *time.Time
		if err := json.Unmarshal(*body.ExpiresAt, &t); err != nil {
			httperror.Write(w, r, fieldErr("expires_at", "an RFC 3339 time or null"))
			return
		}
		if e := checkExpiry(t); e != nil {
			httperror.Write(w, r, e)
			return
		}
		o.ExpiresAt = t
	}
	o, err = h.store().Update(ctx, o)
	if errors.Is(err, flagspkg.ErrNotFound) {
		httperror.Write(w, r, httperror.NotFound("override"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("update override").Wrap(err))
		return
	}
	h.Resolver.Invalidate()
	h.audit(ctx, "flags.override.updated", p.UserID, o.ID, map[string]any{
		"flag_key": o.FlagKey, "scope": o.Scope, "scope_value": o.ScopeValue,
		"value_before": before.Value, "value": o.Value, "expires_at_before": before.ExpiresAt, "expires_at": o.ExpiresAt})
	common.WriteJSON(w, r, http.StatusOK, views([]flagspkg.Override{o}, time.Now())[0])
}

func (h *Handler) deleteOverride(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil || !h.available(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("override"))
		return
	}
	ctx := r.Context()
	o, err := h.store().Get(ctx, id)
	if err == nil {
		err = h.store().Delete(ctx, id)
	}
	if errors.Is(err, flagspkg.ErrNotFound) {
		httperror.Write(w, r, httperror.NotFound("override"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("delete override").Wrap(err))
		return
	}
	h.Resolver.Invalidate()
	h.audit(ctx, "flags.override.deleted", p.UserID, id, map[string]any{
		"flag_key": o.FlagKey, "scope": o.Scope, "scope_value": o.ScopeValue, "value": o.Value})
	common.WriteNoContent(w)
}

func cohortParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := chi.URLParam(r, "cohort")
	if !flagspkg.ValidCohort(name) {
		httperror.Write(w, r, httperror.NotFound("cohort"))
		return "", false
	}
	return name, true
}

func (h *Handler) addToCohort(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil || !h.available(w, r) {
		return
	}
	cohort, ok := cohortParam(w, r)
	if !ok {
		return
	}
	var body struct {
		UserIDs []string `json:"user_ids"`
	}
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if len(body.UserIDs) > MaxBatch {
		httperror.Write(w, r, (&httperror.Error{Type: httperror.TypeBodyTooLarge, Title: "too many user ids",
			Status: http.StatusRequestEntityTooLarge, Detail: "send at most 1000 user ids per request"}).
			With("max_batch", MaxBatch))
		return
	}
	if len(body.UserIDs) == 0 {
		httperror.Write(w, r, fieldErr("user_ids", "at least one user id"))
		return
	}
	ids := make([]string, 0, len(body.UserIDs))
	seen := map[string]bool{}
	for _, raw := range body.UserIDs {
		u, err := uuid.Parse(raw)
		if err != nil {
			httperror.Write(w, r, fieldErr("user_ids", "not a user id: "+raw))
			return
		}
		if id := u.String(); !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	ctx := r.Context()
	exist, err := h.store().ExistingUsers(ctx, ids)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("read users").Wrap(err))
		return
	}
	var missing []string
	for _, id := range ids {
		if !exist[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		httperror.Write(w, r, fieldErr("user_ids", "no such user").With("unknown_user_ids", missing))
		return
	}
	added, err := h.store().AddToCohort(ctx, cohort, ids)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("add to cohort").Wrap(err))
		return
	}
	h.Resolver.Invalidate()
	h.audit(ctx, "flags.cohort.added", p.UserID, cohort, map[string]any{
		"cohort": cohort, "user_ids": ids, "added": added})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"cohort": cohort, "added": added, "requested": len(ids)})
}

func (h *Handler) removeFromCohort(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil || !h.available(w, r) {
		return
	}
	cohort, ok := cohortParam(w, r)
	if !ok {
		return
	}
	uid := chi.URLParam(r, "user_id")
	if _, err := uuid.Parse(uid); err != nil {
		httperror.Write(w, r, httperror.NotFound("cohort member"))
		return
	}
	ctx := r.Context()
	err := h.store().RemoveFromCohort(ctx, cohort, uid)
	if errors.Is(err, flagspkg.ErrNotFound) {
		httperror.Write(w, r, httperror.NotFound("cohort member"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("remove from cohort").Wrap(err))
		return
	}
	h.Resolver.Invalidate()
	h.audit(ctx, "flags.cohort.removed", p.UserID, cohort, map[string]any{"cohort": cohort, "user_id": uid})
	common.WriteNoContent(w)
}

// audit writes an admin write to audit_log (category flags). Best
// effort, as every audit writer here: a miss is logged, never a failed
// request.
func (h *Handler) audit(ctx context.Context, action, actor, target string, payload map[string]any) {
	var who any
	if _, err := uuid.Parse(actor); err == nil {
		who = actor
	}
	blob, _ := json.Marshal(payload)
	if _, err := h.DB.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
		VALUES ('flags', $1, $2, $3, $4)`, action, who, target, string(blob)); err != nil {
		h.Logger.WarnContext(ctx, "flags: audit row not written", "action", action, "err", err, "event", "flags_audit_failed")
	}
}
