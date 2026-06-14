package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Handler bundles deps. Mirrors handlers/collections.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
}

// Mount wires the channel routes. Admin-gating is enforced per-handler
// (mutations are admin-only; reads are ACL-scoped to the principal's
// libraries), matching the collections/libraryacl convention.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/channels", h.List)
	r.Post("/api/channels", h.Create)
	r.Post("/api/channels/reorder", h.Reorder)
	r.Get("/api/channels/{id}", h.Get)
	r.Patch("/api/channels/{id}", h.Patch)
	r.Delete("/api/channels/{id}", h.Delete)
}

func (h *Handler) repo() *repo { return &repo{db: h.DB} }

// List returns channels visible to the principal, with an optional
// now_playing summary per channel.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	f := listFilter{
		libraryID: strings.TrimSpace(r.URL.Query().Get("library_id")),
		category:  strings.TrimSpace(r.URL.Query().Get("category")),
	}
	// `enabled` is tri-state: absent → no filter; present → parsed bool.
	if r.URL.Query().Has("enabled") {
		b, e := common.QueryBool(r, "enabled", true)
		if e != nil {
			httperror.Write(w, r, e)
			return
		}
		f.enabled = &b
	}
	all, err := h.repo().list(r.Context(), f)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list channels"))
		return
	}
	now := h.now()
	items := make([]Channel, 0, len(all))
	for _, c := range all {
		if !h.canRead(p, c.LibraryID) {
			continue
		}
		if np, err := h.repo().nowPlaying(r.Context(), c.ID, now); err == nil && np != nil {
			c.NowPlaying = np
		}
		items = append(items, c)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Create inserts a new channel after validating mode/config/transition
// and deriving a stable, scope-unique slug.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	var req CreateRequest
	if e := common.ReadJSON(r, &req, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Name = strings.TrimSpace(req.Name)

	var fieldErrs []httperror.FieldError
	if req.Name == "" {
		fieldErrs = append(fieldErrs, httperror.FieldError{Field: "name", Message: "required"})
	}
	if err := ValidateMode(req.Mode); err != nil {
		fieldErrs = append(fieldErrs, httperror.FieldError{Field: "mode", Message: "must be one of shuffle, marathon, schedule, smart_mix"})
	}
	if req.LibraryID != nil {
		if _, err := uuid.Parse(*req.LibraryID); err != nil {
			fieldErrs = append(fieldErrs, httperror.FieldError{Field: "library_id", Message: "must be a uuid"})
		}
	}
	if req.Number != nil && *req.Number <= 0 {
		fieldErrs = append(fieldErrs, httperror.FieldError{Field: "number", Message: "must be positive"})
	}
	if err := ValidateTransition(req.Transition); err != nil {
		fieldErrs = append(fieldErrs, httperror.FieldError{Field: "transition", Message: err.Error()})
	}
	if len(fieldErrs) == 0 {
		if err := ValidateModeConfig(req.Mode, req.ModeConfig); err != nil {
			fieldErrs = append(fieldErrs, httperror.FieldError{Field: "mode_config", Message: err.Error()})
		}
	}
	if len(fieldErrs) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(fieldErrs))
		return
	}

	now := h.now()
	c := Channel{
		ID:           uuid.NewString(),
		LibraryID:    req.LibraryID,
		Name:         req.Name,
		Category:     defaultStr(req.Category, "general"),
		Mode:         req.Mode,
		ModeConfig:   req.ModeConfig,
		SourceFilter: req.SourceFilter,
		Transition:   defaultStr(req.Transition, TransitionCut),
		Enabled:      derefBool(req.Enabled, true),
		SortOrder:    derefInt(req.SortOrder, 0),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Stable, scope-unique slug (D4): derive once, suffix on collision.
	slug, err := h.uniqueSlug(r.Context(), req.LibraryID, req.Name)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("derive slug"))
		return
	}
	c.Slug = slug

	// Number: explicit if given, else next free in scope.
	if req.Number != nil {
		c.Number = *req.Number
	} else {
		n, err := h.nextNumber(r.Context(), req.LibraryID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("assign number"))
			return
		}
		c.Number = n
	}

	if err := h.repo().insert(r.Context(), c); err != nil {
		if isUniqueViolation(err) {
			httperror.Write(w, r, httperror.Conflict(
				"https://maktaba.dev/problems/channel-number-exists",
				"a channel with that number already exists in this library"))
			return
		}
		httperror.Write(w, r, httperror.Internal("create channel: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, c)
}

// Get returns one channel (ACL-scoped) with its now_playing summary.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	id := chi.URLParam(r, "id")
	c, err := h.repo().get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("channel "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load channel"))
		return
	}
	if !h.canRead(p, c.LibraryID) {
		httperror.Write(w, r, httperror.Forbidden("", "no access to this channel"))
		return
	}
	if np, err := h.repo().nowPlaying(r.Context(), c.ID, h.now()); err == nil && np != nil {
		c.NowPlaying = np
	}
	common.WriteJSON(w, r, http.StatusOK, c)
}

// Patch updates a subset of fields. A change to mode/mode_config/
// source_filter marks the schedule stale (D5) so the scheduler regens at
// the next boundary — a watching viewer is never yanked mid-program.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	existing, err := h.repo().get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("channel "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load channel"))
		return
	}

	var req PatchRequest
	if e := common.ReadJSON(r, &req, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}

	// Effective mode for config validation (mode may itself be changing).
	effMode := existing.Mode
	if req.Mode != nil {
		effMode = *req.Mode
	}

	var fieldErrs []httperror.FieldError
	if req.Mode != nil {
		if err := ValidateMode(*req.Mode); err != nil {
			fieldErrs = append(fieldErrs, httperror.FieldError{Field: "mode", Message: "must be one of shuffle, marathon, schedule, smart_mix"})
		}
	}
	if req.Transition != nil {
		if err := ValidateTransition(*req.Transition); err != nil {
			fieldErrs = append(fieldErrs, httperror.FieldError{Field: "transition", Message: err.Error()})
		}
	}
	if req.Number != nil && *req.Number <= 0 {
		fieldErrs = append(fieldErrs, httperror.FieldError{Field: "number", Message: "must be positive"})
	}
	if len(req.ModeConfig) > 0 && len(fieldErrs) == 0 {
		if err := ValidateModeConfig(effMode, req.ModeConfig); err != nil {
			fieldErrs = append(fieldErrs, httperror.FieldError{Field: "mode_config", Message: err.Error()})
		}
	}
	if len(fieldErrs) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(fieldErrs))
		return
	}

	sets := []string{}
	args := []any{}
	idx := 1
	add := func(col string, val any) {
		sets = append(sets, col+" = $"+itoa(idx))
		args = append(args, val)
		idx++
	}
	if req.Name != nil {
		add("name", strings.TrimSpace(*req.Name))
	}
	if req.Number != nil {
		add("number", *req.Number)
	}
	if req.Category != nil {
		add("category", *req.Category)
	}
	if req.Mode != nil {
		add("mode", *req.Mode)
	}
	if len(req.ModeConfig) > 0 {
		add("mode_config", string(req.ModeConfig))
	}
	if len(req.SourceFilter) > 0 {
		add("source_filter", string(req.SourceFilter))
	}
	if req.Transition != nil {
		add("transition", *req.Transition)
	}
	if req.Enabled != nil {
		add("enabled", *req.Enabled)
	}
	if req.SortOrder != nil {
		add("sort_order", *req.SortOrder)
	}
	if len(sets) == 0 {
		common.WriteJSON(w, r, http.StatusOK, existing)
		return
	}
	add("updated_at", h.now())
	args = append(args, id)
	q := "UPDATE channels SET " + strings.Join(sets, ", ") + " WHERE id = $" + itoa(idx)
	if _, err := h.DB.ExecContext(r.Context(), q, args...); err != nil {
		if isUniqueViolation(err) {
			httperror.Write(w, r, httperror.Conflict(
				"https://maktaba.dev/problems/channel-number-exists",
				"a channel with that number already exists in this library"))
			return
		}
		httperror.Write(w, r, httperror.Internal("update channel: "+err.Error()))
		return
	}

	// D5: a rule change (mode / mode_config / source_filter) marks the
	// schedule stale so the scheduler regens from the next boundary.
	// Best-effort: missing channel_schedule_state (27.2 not yet applied)
	// is not an error.
	if req.Mode != nil || len(req.ModeConfig) > 0 || len(req.SourceFilter) > 0 {
		h.markStale(r.Context(), id)
	}

	updated, err := h.repo().get(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("reload channel"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, updated)
}

// Delete drops the channel (cascade clears its schedule + runtime).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	if err := h.repo().delete(r.Context(), id); err != nil {
		httperror.Write(w, r, httperror.Internal("delete channel"))
		return
	}
	common.WriteNoContent(w)
}

// Reorder applies a bulk renumber atomically (D3): either every (id,
// number) pair lands or none does. A duplicate number within scope trips
// the partial unique index and rolls the whole batch back.
func (h *Handler) Reorder(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	var entries []ReorderEntry
	if e := common.ReadJSON(r, &entries, 256<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if err := validateReorder(entries); err != nil {
		httperror.Write(w, r, httperror.BadRequest(err.Error()))
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx"))
		return
	}
	defer func() { _ = tx.Rollback() }()

	now := h.now()
	for _, e := range entries {
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE channels SET number = $1, updated_at = $2 WHERE id = $3`,
			e.Number, now, e.ID); err != nil {
			if isUniqueViolation(err) {
				httperror.Write(w, r, httperror.Conflict(
					"https://maktaba.dev/problems/channel-number-exists",
					"reorder would collide on a channel number"))
				return
			}
			httperror.Write(w, r, httperror.Internal("reorder: "+err.Error()))
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit reorder"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"reordered": len(entries)})
}

// --- helpers -------------------------------------------------------------

// uniqueSlug derives a stable base slug from the name and suffixes a
// counter until it is free within scope (D4).
func (h *Handler) uniqueSlug(ctx context.Context, libraryID *string, name string) (string, error) {
	base := Slugify(name)
	for n := 1; n <= 1000; n++ {
		cand := SlugWithSuffix(base, n)
		taken, err := h.repo().slugTaken(ctx, libraryID, cand)
		if err != nil {
			return "", err
		}
		if !taken {
			return cand, nil
		}
	}
	// Fall back to a uuid-suffixed slug rather than loop forever.
	return base + "-" + uuid.NewString()[:8], nil
}

// nextNumber returns the lowest unused dial position in scope (defaults
// to 1 on an empty/missing scope).
func (h *Handler) nextNumber(ctx context.Context, libraryID *string) (int, error) {
	var maxNum sql.NullInt64
	var err error
	if libraryID == nil {
		err = h.DB.QueryRowContext(ctx,
			`SELECT max(number) FROM channels WHERE library_id IS NULL`).Scan(&maxNum)
	} else {
		err = h.DB.QueryRowContext(ctx,
			`SELECT max(number) FROM channels WHERE library_id = $1`, *libraryID).Scan(&maxNum)
	}
	if err != nil {
		return 0, err
	}
	if !maxNum.Valid {
		return 1, nil
	}
	return int(maxNum.Int64) + 1, nil
}

// markStale flips channel_schedule_state.stale; best-effort (27.2 may not
// be applied yet). Inserts the state row if absent so the scheduler sees
// the flag on first run.
func (h *Handler) markStale(ctx context.Context, channelID string) {
	_, _ = h.DB.ExecContext(ctx, `
		INSERT INTO channel_schedule_state (channel_id, stale)
		VALUES ($1, true)
		ON CONFLICT (channel_id) DO UPDATE SET stale = true
	`, channelID)
}

// canRead reports whether the principal may see a channel in the given
// scope. A multi-library (nil) channel is visible to any authenticated
// principal; a library-scoped one requires access to that library.
func (h *Handler) canRead(p *principal.Principal, libraryID *string) bool {
	if p.AccessAllLibraries || p.IsAdmin {
		return true
	}
	if libraryID == nil {
		return true
	}
	return p.HasLibrary(*libraryID)
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

// validateReorder rejects empty payloads, missing ids, duplicate ids, and
// non-positive numbers before any DB work (D3 — caught client-side fast).
func validateReorder(entries []ReorderEntry) error {
	if len(entries) == 0 {
		return errors.New("reorder requires at least one entry")
	}
	seenID := map[string]struct{}{}
	seenNum := map[int]struct{}{}
	for _, e := range entries {
		if strings.TrimSpace(e.ID) == "" {
			return errors.New("each entry requires an id")
		}
		if e.Number <= 0 {
			return errors.New("number must be positive")
		}
		if _, dup := seenID[e.ID]; dup {
			return errors.New("duplicate id in reorder payload")
		}
		if _, dup := seenNum[e.Number]; dup {
			return errors.New("duplicate number in reorder payload")
		}
		seenID[e.ID] = struct{}{}
		seenNum[e.Number] = struct{}{}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique") || strings.Contains(s, "duplicate")
}

func titleFromSnapshot(snap []byte) string {
	if len(snap) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(snap, &m); err != nil {
		return ""
	}
	if t, ok := m["title"].(string); ok {
		return t
	}
	return ""
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func nullStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func jsonOrEmpty(b json.RawMessage) any {
	if len(b) == 0 {
		return "{}"
	}
	return string(b)
}

func jsonOrNull(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func defaultStr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func derefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func derefInt(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
