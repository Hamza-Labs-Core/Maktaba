package libraries

// Media permissions (Epic 59, story 59.03):
//
//	GET  /api/libraries/{id}/access          the last check, per root, with the fix (admin)
//	POST /api/libraries/{id}/check-access    check now (admin; the page's Check access)
//
// A library is checked when it is added, when its roots change, from Check access and, after
// a write failed with a permission error (the pipeline records it and asks for a check), at
// the next read of its access. Never at start: the probe writes, which would wake a sleeping
// disk (shared/go/mediaaccess).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/libpaths"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/mediaaccess"
)

// AccessChecker checks one root (mediaaccess.Check in production; a fake in tests).
type AccessChecker interface {
	Check(root string) mediaaccess.Result
}

type osAccessChecker struct{}

func (osAccessChecker) Check(root string) mediaaccess.Result { return mediaaccess.Check(root) }

// WriteFailure is a write that failed with a permission error after the last check.
type WriteFailure struct {
	Path string    `json:"path"`
	At   time.Time `json:"at"`
}

// accessDetail is libraries.access_detail.
type accessDetail struct {
	Roots         []mediaaccess.Result `json:"roots,omitempty"`
	WriteFailures []WriteFailure       `json:"write_failures,omitempty"`
	// RecheckAfter: a write failed at this time; the next read of the access checks again
	// when the last check is older.
	RecheckAfter *time.Time `json:"recheck_after,omitempty"`
}

func parseDetail(raw []byte) accessDetail {
	var d accessDetail
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &d)
	}
	return d
}

// partial: a root's sample found unreadable files, or a write failed since.
func (d accessDetail) partial() bool {
	if len(d.WriteFailures) > 0 {
		return true
	}
	for _, r := range d.Roots {
		if r.Partial {
			return true
		}
	}
	return false
}

// RootAccess is one root in an AccessReport.
type RootAccess struct {
	mediaaccess.Result
	// Fix is what to change, in the platform's words; nil when the root needs nothing.
	Fix *mediaaccess.Fix `json:"fix"`
}

// AccessReport is the library page's access panel.
type AccessReport struct {
	LibraryID     string             `json:"library_id"`
	Access        *string            `json:"access"`
	Partial       bool               `json:"partial"`
	CheckedAt     *time.Time         `json:"checked_at"`
	Roots         []RootAccess       `json:"roots"`
	WriteFailures []WriteFailure     `json:"write_failures"`
	RunsAs        mediaaccess.RunsAs `json:"runs_as"`
	Platform      string             `json:"platform"`
}

func (h *Handler) accessChecker() AccessChecker {
	if h.Access != nil {
		return h.Access
	}
	return osAccessChecker{}
}

func (h *Handler) platform() string {
	if h.Platform != nil {
		return h.Platform()
	}
	return os.Getenv("MAKTABA_PLATFORM")
}

func (h *Handler) runsAs() mediaaccess.RunsAs {
	if h.RunsAs != nil {
		return h.RunsAs()
	}
	return mediaaccess.CurrentUser()
}

// checkRootsAccess checks every root at once (each where this machine reaches it) and
// returns the results in the roots' order, each named by the root as stored.
func (h *Handler) checkRootsAccess(roots []string) []mediaaccess.Result {
	out := make([]mediaaccess.Result, len(roots))
	checker := h.accessChecker()
	var wg sync.WaitGroup
	for i, root := range roots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := checker.Check(libpaths.ToLocal(root))
			res.Path = root
			if res.Unreadable == nil {
				res.Unreadable = []string{}
			}
			out[i] = res
		}()
	}
	wg.Wait()
	return out
}

// storeAccess checks roots and stores the result on library id. keepFailures keeps the
// writes that failed since the last check (a check a failure asked for); a check someone
// asked for starts afresh. It returns the stored access and detail.
func (h *Handler) storeAccess(ctx context.Context, id string, roots []string, keepFailures bool) (string, accessDetail, time.Time, error) {
	results := h.checkRootsAccess(roots)
	access := []mediaaccess.Access{}
	for _, r := range results {
		access = append(access, r.Access)
	}
	worst := string(mediaaccess.Worst(access...))
	if worst == "" {
		worst = string(mediaaccess.None)
	}
	d := accessDetail{Roots: results}
	if keepFailures {
		var raw []byte
		if err := h.DB.QueryRowContext(ctx, `SELECT access_detail FROM libraries WHERE id = $1`, id).Scan(&raw); err != nil &&
			!errors.Is(err, sql.ErrNoRows) {
			return "", d, time.Time{}, err
		}
		d.WriteFailures = parseDetail(raw).WriteFailures
	}
	b, err := json.Marshal(d)
	if err != nil {
		return "", d, time.Time{}, err
	}
	now := h.now()
	if _, err := h.DB.ExecContext(ctx, `UPDATE libraries SET access = $2, access_checked_at = $3, access_detail = $4
		WHERE id = $1`, id, worst, now, string(b)); err != nil {
		return "", d, time.Time{}, err
	}
	return worst, d, now, nil
}

// applyAccess runs the check for a library just saved and sets its fields on lib; a check
// that cannot be stored is logged in the response as no access information, never a failed
// save (the library is saved; Check access tries again).
func (h *Handler) applyAccess(ctx context.Context, lib *Library) {
	access, d, at, err := h.storeAccess(ctx, lib.ID, lib.Roots, false)
	if err != nil {
		return
	}
	lib.Access, lib.AccessCheckedAt, lib.AccessPartial = &access, &at, d.partial()
}

// report builds the access panel from the stored row.
func (h *Handler) report(id string, access sql.NullString, checked sql.NullTime, d accessDetail) AccessReport {
	rep := AccessReport{LibraryID: id, Roots: []RootAccess{}, WriteFailures: []WriteFailure{},
		RunsAs: h.runsAs(), Platform: h.platform(), Partial: d.partial()}
	if access.Valid {
		rep.Access = &access.String
	}
	if checked.Valid {
		t := checked.Time
		rep.CheckedAt = &t
	}
	for _, r := range d.Roots {
		ra := RootAccess{Result: r}
		if r.Access != mediaaccess.ReadWrite || r.Partial {
			f := mediaaccess.FixFor(rep.Platform, r.Path, rep.RunsAs)
			ra.Fix = &f
		}
		rep.Roots = append(rep.Roots, ra)
	}
	if d.WriteFailures != nil {
		rep.WriteFailures = d.WriteFailures
	}
	return rep
}

func (h *Handler) loadAccess(ctx context.Context, id string) (roots []string, access sql.NullString, checked sql.NullTime, d accessDetail, err error) {
	var rootsArr stringArray
	var raw []byte
	err = h.DB.QueryRowContext(ctx, `SELECT roots, access, access_checked_at, access_detail FROM libraries WHERE id = $1`, id).
		Scan(&rootsArr, &access, &checked, &raw)
	return rootsArr, access, checked, parseDetail(raw), err
}

// Access implements GET /api/libraries/{id}/access. A write that failed with a permission
// error after the last check makes this read check again first.
func (h *Handler) GetAccess(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	id, ok := libraryID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	roots, access, checked, d, err := h.loadAccess(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("library "+id))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load library access").Wrap(err))
		return
	}
	if d.RecheckAfter != nil && (!checked.Valid || checked.Time.Before(*d.RecheckAfter)) {
		a, nd, at, err := h.storeAccess(ctx, id, roots, true)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("check library access").Wrap(err))
			return
		}
		access, checked, d = sql.NullString{String: a, Valid: true}, sql.NullTime{Time: at, Valid: true}, nd
	}
	common.WriteJSON(w, r, http.StatusOK, h.report(id, access, checked, d))
}

// CheckAccess implements POST /api/libraries/{id}/check-access: the page's Check access. A
// library whose access changed (ro → rw after the household's fix) is picked up here;
// nothing needs adding again.
func (h *Handler) CheckAccess(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := libraryID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	roots, _, _, _, err := h.loadAccess(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("library "+id))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load library access").Wrap(err))
		return
	}
	access, d, at, err := h.storeAccess(ctx, id, roots, false)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("check library access").Wrap(err))
		return
	}
	WriteAudit(ctx, h.DB, EventAccessChecked, ActorOf(p), id, map[string]any{"access": access, "partial": d.partial()})
	common.WriteJSON(w, r, http.StatusOK, h.report(id, sql.NullString{String: access, Valid: true},
		sql.NullTime{Time: at, Valid: true}, d))
}
