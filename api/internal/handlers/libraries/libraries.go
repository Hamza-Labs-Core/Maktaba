// Package libraries implements the Story 7.3 endpoints:
//
//	GET    /api/libraries
//	POST   /api/libraries
//	GET    /api/libraries/{id}
//	PATCH  /api/libraries/{id}
//	DELETE /api/libraries/{id}
//	POST   /api/libraries/{id}/scan
//	GET    /api/libraries/{id}/stats
//
// Validation lives here (root path checks, name uniqueness translation),
// the SQL is one round-trip per operation, and destructive operations
// (DELETE ?purge=true) require a name-match confirmation per AC-4.
package libraries

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Library is the over-the-wire shape returned by every endpoint here.
// Settings is kept as raw JSON so a PATCH can deep-merge without
// imposing a Go schema on user-configurable knobs.
type Library struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Roots     []string        `json:"roots"`
	Settings  json.RawMessage `json:"settings"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// CreateRequest is the POST body — name + roots are required.
type CreateRequest struct {
	Name     string          `json:"name"`
	Roots    []string        `json:"roots"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

// PatchRequest is the PATCH body; all fields optional.
type PatchRequest struct {
	Name     *string         `json:"name,omitempty"`
	Roots    *[]string       `json:"roots,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

// Handler bundles handler dependencies. DB is mandatory; PathChecker
// is injectable so tests can stub the filesystem checks.
type Handler struct {
	DB          *sql.DB
	PathChecker PathChecker
	JobEnqueuer JobEnqueuer
	NowFunc     func() time.Time
}

// PathChecker validates that a library root is an absolute, existing,
// readable directory. The real implementation calls os.Stat; tests
// inject a fake.
type PathChecker interface {
	Check(root string) error
}

// JobEnqueuer is the surface for “POST /api/libraries/{id}/scan“ —
// scan-job insert + NOTIFY. Story 6.x owns the schema; this story only
// fires the insert.
type JobEnqueuer interface {
	EnqueueScan(ctx context.Context, libraryID string, priority int) (jobID string, err error)
}

// OSPathChecker is the production PathChecker (Story 7.3 AC-2): absolute
// path + os.Stat must succeed + the inode must be readable.
type OSPathChecker struct{}

// Check returns nil if root is absolute, exists, is a directory, and is
// readable by this process. Otherwise it returns a sentinel error whose
// string matches the AC vocabulary: “not-absolute“, “not-found“,
// “not-readable“.
func (OSPathChecker) Check(root string) error {
	if !filepath.IsAbs(root) {
		return errPathNotAbsolute
	}
	fi, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return errPathNotFound
		}
		return errPathNotReadable
	}
	if !fi.IsDir() {
		return errPathNotReadable
	}
	// A best-effort readable check: try to open. ReadDir is heavier and
	// we don't actually need the listing.
	f, err := os.Open(root)
	if err != nil {
		return errPathNotReadable
	}
	_ = f.Close()
	return nil
}

var (
	errPathNotAbsolute = errors.New("not-absolute")
	errPathNotFound    = errors.New("not-found")
	errPathNotReadable = errors.New("not-readable")
)

// Mount wires the handler set onto r. All routes are JSON; auth is
// expected to be applied by an outer middleware.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/libraries", h.List)
	r.Post("/api/libraries", h.Create)
	r.Get("/api/libraries/{id}", h.Get)
	r.Patch("/api/libraries/{id}", h.Patch)
	r.Delete("/api/libraries/{id}", h.Delete)
	r.Post("/api/libraries/{id}/scan", h.Scan)
	// Story 9.7 AC-1: the cache-first StatsCached handler is the
	// AC-compliant one (full by_content_type/storage/jobs/last_sweep
	// shape, processed_pct=null for empty libraries). The Phase-3
	// `Stats` handler (libraries.go) is kept as a legacy direct-from-
	// videos fallback for environments where library_stats_cache has
	// not been migrated, but production mounts StatsCached.
	r.Get("/api/libraries/{id}/stats", h.StatsCached)
}

// List returns every library the principal can read. For an admin /
// single-user this is the full table; for a scoped user it's the
// intersection with their “lib[]“ claim.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, &httperror.Error{Type: httperror.TypeForbidden, Title: "unauthorized", Status: http.StatusUnauthorized, Detail: "authentication required"})
		return
	}

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT id, name, roots, settings, created_at, updated_at
		FROM libraries
		ORDER BY created_at DESC
	`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("query libraries"))
		return
	}
	defer rows.Close()

	out := []Library{}
	for rows.Next() {
		var lib Library
		var rootsArr stringArray
		var settings []byte
		if err := rows.Scan(&lib.ID, &lib.Name, &rootsArr, &settings, &lib.CreatedAt, &lib.UpdatedAt); err != nil {
			httperror.Write(w, r, httperror.Internal("scan libraries row"))
			return
		}
		lib.Roots = rootsArr
		if len(settings) == 0 {
			settings = []byte(`{}`)
		}
		lib.Settings = settings
		if p.AccessAllLibraries || p.HasLibrary(lib.ID) {
			out = append(out, lib)
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// Create implements AC-1 + AC-2: validate, dedup, refuse overlap, insert.
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
	if req.Name == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "name", Message: "required"}}))
		return
	}
	if len(req.Roots) == 0 {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "roots", Message: "at least one root required"}}))
		return
	}

	roots, fieldErrs := h.normaliseAndValidateRoots(req.Roots)
	if len(fieldErrs) > 0 {
		e := httperror.Unprocessable(fieldErrs)
		e.Type = "https://maktaba.dev/problems/library-roots-invalid"
		httperror.Write(w, r, e)
		return
	}

	if err := h.checkRootOverlap(r.Context(), "", roots); err != nil {
		httperror.Write(w, r, err)
		return
	}

	settings := req.Settings
	if len(settings) == 0 {
		settings = []byte(`{}`)
	}
	// Story 9.1 AC-1: same schema gate as PATCH — a create that ships a
	// malformed `settings` blob is a 422 with the offending path.
	if len(req.Settings) > 0 {
		var decoded map[string]any
		if jErr := json.Unmarshal(settings, &decoded); jErr != nil {
			httperror.Write(w, r, httperror.BadRequest("settings is not a JSON object"))
			return
		}
		if fieldErrs, _ := ValidateLibrarySettings(decoded); len(fieldErrs) > 0 {
			e := httperror.Unprocessable(fieldErrs)
			e.Type = "https://maktaba.dev/problems/library-settings-invalid"
			httperror.Write(w, r, e)
			return
		}
	}
	id := uuid.NewString()
	now := h.now()
	_, err := h.DB.ExecContext(r.Context(), `
		INSERT INTO libraries (id, name, roots, settings, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
	`, id, req.Name, stringArray(roots), string(settings), now)
	if err != nil {
		if isUniqueViolation(err, "name") {
			httperror.Write(w, r, &httperror.Error{
				Type:   "https://maktaba.dev/problems/library-name-exists",
				Title:  "name already exists",
				Status: http.StatusConflict,
				Detail: req.Name + " is already in use",
			})
			return
		}
		httperror.Write(w, r, httperror.Internal("insert library"))
		return
	}

	w.Header().Set("Location", "/api/libraries/"+id)
	common.WriteJSON(w, r, http.StatusCreated, Library{
		ID: id, Name: req.Name, Roots: roots, Settings: settings,
		CreatedAt: now, UpdatedAt: now,
	})
}

// Get returns the library row or 404.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	lib, err := h.loadLibrary(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library"))
		return
	}
	p := principal.FromContext(r.Context())
	if p == nil || (!p.AccessAllLibraries && !p.HasLibrary(id)) {
		httperror.Write(w, r, httperror.Forbidden("", ""))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, lib)
}

// Patch implements AC-3 deep-merge for settings and refuses bad roots.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}

	var req PatchRequest
	if e := common.ReadJSON(r, &req, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx begin"))
		return
	}
	defer func() { _ = tx.Rollback() }()

	var cur Library
	var rootsArr stringArray
	var settingsRaw []byte
	err = tx.QueryRowContext(r.Context(), `
		SELECT id, name, roots, settings, created_at, updated_at FROM libraries WHERE id=$1
	`, id).Scan(&cur.ID, &cur.Name, &rootsArr, &settingsRaw, &cur.CreatedAt, &cur.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library"))
		return
	}
	cur.Roots = rootsArr

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "name", Message: "required"}}))
			return
		}
		cur.Name = name
	}
	if req.Roots != nil {
		roots, fieldErrs := h.normaliseAndValidateRoots(*req.Roots)
		if len(fieldErrs) > 0 {
			e := httperror.Unprocessable(fieldErrs)
			e.Type = "https://maktaba.dev/problems/library-roots-invalid"
			httperror.Write(w, r, e)
			return
		}
		if err := h.checkRootOverlap(r.Context(), id, roots); err != nil {
			httperror.Write(w, r, err)
			return
		}
		cur.Roots = roots
	}

	if len(settingsRaw) == 0 {
		settingsRaw = []byte(`{}`)
	}
	merged := settingsRaw
	if len(req.Settings) > 0 {
		newSettings, mergeErr := DeepMergeJSON(settingsRaw, req.Settings)
		if mergeErr != nil {
			httperror.Write(w, r, httperror.BadRequest("settings is not a JSON object: "+mergeErr.Error()))
			return
		}
		merged = newSettings

		// Story 9.1 AC-1: validate the *merged* effective settings —
		// a PATCH that introduces e.g. stt.backend="invalid" is a 422
		// with the offending JSON path, not a silent 200. Unknown keys
		// are not fatal (forward-compat: they round-trip with a warning
		// surfaced in the response).
		var decoded map[string]any
		if jErr := json.Unmarshal(merged, &decoded); jErr != nil {
			httperror.Write(w, r, httperror.BadRequest("settings is not a JSON object"))
			return
		}
		if fieldErrs, _ := ValidateLibrarySettings(decoded); len(fieldErrs) > 0 {
			e := httperror.Unprocessable(fieldErrs)
			e.Type = "https://maktaba.dev/problems/library-settings-invalid"
			httperror.Write(w, r, e)
			return
		}
	}
	cur.Settings = merged

	now := h.now()
	_, err = tx.ExecContext(r.Context(), `
		UPDATE libraries SET name=$1, roots=$2, settings=$3, updated_at=$4 WHERE id=$5
	`, cur.Name, stringArray(cur.Roots), string(merged), now, id)
	if err != nil {
		if isUniqueViolation(err, "name") {
			httperror.Write(w, r, &httperror.Error{
				Type:   "https://maktaba.dev/problems/library-name-exists",
				Title:  "name already exists",
				Status: http.StatusConflict,
				Detail: cur.Name + " is already in use",
			})
			return
		}
		httperror.Write(w, r, httperror.Internal("update library"))
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit"))
		return
	}
	cur.UpdatedAt = now
	common.WriteJSON(w, r, http.StatusOK, cur)
}

// Delete implements AC-4: ?purge=false (default) cascades DB rows; ?purge=true
// also requires “?confirm=<name>“ and unlinks the on-disk files.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}

	lib, err := h.loadLibrary(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library"))
		return
	}

	purge, e := common.QueryBool(r, "purge", false)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if purge {
		confirm := r.URL.Query().Get("confirm")
		if confirm != lib.Name {
			httperror.Write(w, r, &httperror.Error{
				Type:   httperror.TypeConfirmationReq,
				Title:  "confirmation required",
				Status: http.StatusPreconditionFailed,
				Detail: "include ?confirm=<library name> to purge",
			})
			return
		}
	}

	if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM libraries WHERE id=$1`, id); err != nil {
		httperror.Write(w, r, httperror.Internal("delete library"))
		return
	}
	if purge {
		failed := []string{}
		for _, root := range lib.Roots {
			if err := os.RemoveAll(root); err != nil {
				failed = append(failed, root)
			}
		}
		if len(failed) > 0 {
			common.WriteJSON(w, r, http.StatusMultiStatus, map[string]any{
				"deleted":      true,
				"failed_paths": failed,
			})
			return
		}
	}
	common.WriteNoContent(w)
}

// Scan implements AC-5: enqueue a scan job at priority 50.
func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	if _, err := h.loadLibrary(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library"))
		return
	}
	if h.JobEnqueuer == nil {
		// Soft-fail mode: report 202 with an empty job_id so the route
		// is callable in environments where the job queue isn't wired
		// (CI / dev unit tests). Production must inject a real enqueuer.
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"job_id": ""})
		return
	}
	jobID, err := h.JobEnqueuer.EnqueueScan(r.Context(), id, 50)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("enqueue scan: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"job_id": jobID})
}

// StatsResponse is the AC-6 shape: derived counts and groupings.
type StatsResponse struct {
	TotalVideos      int            `json:"total_videos"`
	TotalDurationSec float64        `json:"total_duration_sec"`
	ByState          map[string]int `json:"by_state"`
	ProcessedPct     float64        `json:"processed_pct"`
	ByLanguage       map[string]int `json:"by_language"`
}

// Stats implements AC-6 with a single SQL round-trip per group.
// State counts use “GROUP BY state“; language counts use “GROUP BY
// detected_language“. “processed_pct“ is the share of videos whose
// state is in the terminal-success set.
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	if _, err := h.loadLibrary(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library"))
		return
	}

	resp := StatsResponse{ByState: map[string]int{}, ByLanguage: map[string]int{}}

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT state, COUNT(*), COALESCE(SUM(duration_sec), 0)
		FROM videos WHERE library_id = $1
		GROUP BY state
	`, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("stats by_state"))
		return
	}
	defer rows.Close()
	var done float64
	for rows.Next() {
		var state string
		var cnt int
		var dur float64
		if err := rows.Scan(&state, &cnt, &dur); err != nil {
			httperror.Write(w, r, httperror.Internal("stats by_state scan"))
			return
		}
		resp.ByState[state] = cnt
		resp.TotalVideos += cnt
		resp.TotalDurationSec += dur
		if state == "ready" || state == "transcribed" || state == "indexed" {
			done += float64(cnt)
		}
	}
	if resp.TotalVideos > 0 {
		resp.ProcessedPct = (done / float64(resp.TotalVideos)) * 100.0
	}

	rows2, err := h.DB.QueryContext(r.Context(), `
		SELECT COALESCE(detected_language, ''), COUNT(*)
		FROM videos WHERE library_id = $1
		GROUP BY detected_language
	`, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("stats by_language"))
		return
	}
	defer rows2.Close()
	for rows2.Next() {
		var lang string
		var cnt int
		if err := rows2.Scan(&lang, &cnt); err != nil {
			httperror.Write(w, r, httperror.Internal("stats by_language scan"))
			return
		}
		if lang == "" {
			lang = "unknown"
		}
		resp.ByLanguage[lang] = cnt
	}

	common.WriteJSON(w, r, http.StatusOK, resp)
}

// loadLibrary fetches a single library row; sql.ErrNoRows is returned
// verbatim so callers can map to 404.
func (h *Handler) loadLibrary(ctx context.Context, id string) (Library, error) {
	var lib Library
	var rootsArr stringArray
	var settings []byte
	err := h.DB.QueryRowContext(ctx, `
		SELECT id, name, roots, settings, created_at, updated_at FROM libraries WHERE id=$1
	`, id).Scan(&lib.ID, &lib.Name, &rootsArr, &settings, &lib.CreatedAt, &lib.UpdatedAt)
	if err != nil {
		return Library{}, err
	}
	lib.Roots = rootsArr
	if len(settings) == 0 {
		settings = []byte(`{}`)
	}
	lib.Settings = settings
	return lib, nil
}

// normaliseAndValidateRoots dedups, then runs the PathChecker.
// Returns (cleaned roots, field errors). The field errors use the
// `roots[i]` JSON-pointer convention so the UI can highlight the bad
// entry.
func (h *Handler) normaliseAndValidateRoots(in []string) ([]string, []httperror.FieldError) {
	seen := map[string]struct{}{}
	cleaned := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue // silent dedup per EC
		}
		seen[p] = struct{}{}
		cleaned = append(cleaned, p)
	}
	checker := h.PathChecker
	if checker == nil {
		checker = OSPathChecker{}
	}
	var errs []httperror.FieldError
	for i, p := range cleaned {
		if err := checker.Check(p); err != nil {
			errs = append(errs, httperror.FieldError{
				Field:   "roots[" + itoa(i) + "]",
				Message: err.Error(),
			})
		}
	}
	return cleaned, errs
}

// checkRootOverlap rejects POST/PATCH when a root nests inside another
// library's roots (or vice-versa). exceptID skips the row being edited
// during a PATCH.
func (h *Handler) checkRootOverlap(ctx context.Context, exceptID string, roots []string) *httperror.Error {
	rows, err := h.DB.QueryContext(ctx, `SELECT id, roots FROM libraries`)
	if err != nil {
		return httperror.Internal("overlap check")
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var existing stringArray
		if err := rows.Scan(&id, &existing); err != nil {
			return httperror.Internal("overlap scan")
		}
		if id == exceptID {
			continue
		}
		for _, a := range roots {
			for _, b := range existing {
				if pathsOverlap(a, b) {
					return &httperror.Error{
						Type:   "https://maktaba.dev/problems/library-roots-overlap",
						Title:  "library roots overlap",
						Status: http.StatusUnprocessableEntity,
						Detail: a + " overlaps existing library root " + b,
					}
				}
			}
		}
	}
	return nil
}

// pathsOverlap reports whether one of (a, b) contains the other or
// they are identical. Trailing slashes are normalised away first.
func pathsOverlap(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == b {
		return true
	}
	if strings.HasPrefix(a, b+string(filepath.Separator)) {
		return true
	}
	if strings.HasPrefix(b, a+string(filepath.Separator)) {
		return true
	}
	return false
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
