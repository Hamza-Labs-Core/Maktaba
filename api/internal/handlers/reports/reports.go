// Package reports serves the HTML reports the pipeline writes into its
// reports folder (default ~/.maktaba/cache/reviews, beside the thumbnail
// cache) and drives the channel plan preview:
//
//	GET  /api/reports                       the report files, with kind and title
//	GET  /api/reports/{name}                one report page (CSP: no script)
//	GET  /api/thumbnails/{hash}/small.jpg   the small thumbnail a review page links
//	GET  /api/reports/channel-plan/preview  state of the background plan preview
//	POST /api/reports/channel-plan/preview  admin: start a preview or render the stored plan
//
// The pages are the stand-alone control centre's reports ported to Maktaba:
// one review page per library (review_pages stage), channel-plan.html (the
// plan_channels stage), channel-plan-preview.html (the dry run) and
// channel-audit.html (the audit stage). Any other *.html in the folder is
// listed and served the same way.
//
// Security. A request names a file; it never names a path. The name must
// match a strict pattern AND be a regular file found by listing the folder,
// so "..", separators, symlinks and temp files (".part.") are all 404. Every
// page is served with a Content-Security-Policy that allows no script, form
// or network access (images only from this origin), nosniff and no referrer;
// the web app shows them in a sandboxed iframe. Reads need a signed-in
// principal and follow the library ACL: a review page needs read access to
// its library, the collection-wide pages (plan, preview, audit, other) an
// admin or all-library principal. Starting a preview needs an admin.
package reports

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/pagetheme"
)

// Well-known page names (the pipeline writes them; keep in step with
// engine/plan_page.py and the audit stage).
const (
	PlanPage    = "channel-plan.html"
	PreviewPage = "channel-plan-preview.html"
	AuditPage   = "channel-audit.html"
)

// Report kinds returned by GET /api/reports.
const (
	KindReview  = "review"
	KindPlan    = "plan"
	KindPreview = "preview"
	KindAudit   = "audit"
	KindOther   = "other"
)

// pageCSP lets a report use inline styles and images from this origin and
// nothing else: no script, no form, no frame, no connection, no <base>.
const pageCSP = "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

var (
	// nameRe is the only shape of file name a request may use.
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.html$`)
	// hashRe matches the thumbnail cache's folder names (content hashes).
	hashRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Handler bundles the dependencies of the report routes.
type Handler struct {
	DB *sql.DB
	// Dir is the reports folder; "" resolves Root() at request time.
	Dir string
	// ThumbDir is the thumbnail cache; "" resolves ThumbnailRoot().
	ThumbDir string
	// Pipeline is the pipeline gRPC client; the plan preview works only when
	// it implements pipeline.PlanPreviewer (nil / offline -> 503).
	Pipeline any
}

// ThumbnailRoot is the pipeline's thumbnail cache: MAKTABA_THUMBNAIL_ROOT,
// else ~/.maktaba/cache/thumbnails (the same default the pipeline uses).
func ThumbnailRoot() string {
	if v := os.Getenv("MAKTABA_THUMBNAIL_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".maktaba", "cache", "thumbnails")
}

// Root is the reports folder: MAKTABA_REVIEWS_ROOT, else "reviews" beside the
// thumbnail cache (what engine/stages/review_pages.reviews_dir resolves).
func Root() string {
	if v := os.Getenv("MAKTABA_REVIEWS_ROOT"); v != "" {
		return v
	}
	t := ThumbnailRoot()
	if t == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(t), "reviews")
}

func (h *Handler) dir() string {
	if h.Dir != "" {
		return h.Dir
	}
	return Root()
}

func (h *Handler) thumbDir() string {
	if h.ThumbDir != "" {
		return h.ThumbDir
	}
	return ThumbnailRoot()
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/reports", h.List)
	r.Get("/api/reports/channel-plan/preview", h.PreviewStatus)
	r.Post("/api/reports/channel-plan/preview", h.PreviewStart)
	r.Get("/api/reports/{name}", h.Page)
	r.Get("/api/thumbnails/{hash}/small.jpg", h.Thumbnail)
}

func requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if principal.FromContext(r.Context()) == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return false
	}
	return true
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

// canSee applies the per-principal library ACL to a report: a review page
// belongs to its library and needs read access to it (as the channels
// handler's canRead); every other page (plan, preview, audit, other) spans
// the whole collection, so it needs an admin or an all-library principal.
func canSee(p *principal.Principal, name string) bool {
	if p == nil {
		return false
	}
	if p.IsAdmin || p.AccessAllLibraries {
		return true
	}
	if kindOf(name) == KindReview {
		return p.HasLibrary(strings.ToLower(strings.TrimSuffix(name, ".html")))
	}
	return false
}

// Report is one GET /api/reports item.
type Report struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	LibraryID *string   `json:"library_id"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updated_at"`
}

// kindOf classifies a file name; a review page is "<library uuid>.html".
func kindOf(name string) string {
	switch name {
	case PlanPage:
		return KindPlan
	case PreviewPage:
		return KindPreview
	case AuditPage:
		return KindAudit
	}
	if uuidRe.MatchString(strings.TrimSuffix(name, ".html")) {
		return KindReview
	}
	return KindOther
}

// listing returns the servable files of the folder: regular files whose
// name matches nameRe and is not a temp file. A missing folder is empty.
func (h *Handler) listing() (map[string]os.FileInfo, error) {
	dir := h.dir()
	out := map[string]os.FileInfo{}
	if dir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if !nameRe.MatchString(name) || strings.Contains(name, ".part.") || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out[name] = info
	}
	return out, nil
}

// libraryNames maps library ids to their names for the review pages.
func (h *Handler) libraryNames(ctx context.Context, ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 || h.DB == nil {
		return out
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT id::text, name FROM libraries WHERE id::text = ANY($1)`, pq.Array(ids))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) == nil {
			out[id] = name
		}
	}
	return out
}

var fixedTitles = map[string]string{
	PlanPage:    "Channel plan",
	PreviewPage: "Channel plan (preview)",
	AuditPage:   "Channel audit",
}

// List implements GET /api/reports.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	files, err := h.listing()
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list reports: "+err.Error()))
		return
	}
	p := principal.FromContext(r.Context())
	for name := range files {
		if !canSee(p, name) {
			delete(files, name)
		}
	}
	var libs []string
	for name := range files {
		if kindOf(name) == KindReview {
			libs = append(libs, strings.ToLower(strings.TrimSuffix(name, ".html")))
		}
	}
	names := h.libraryNames(r.Context(), libs)
	out := make([]Report, 0, len(files))
	for name, info := range files {
		rep := Report{Name: name, Kind: kindOf(name), Title: name, Size: info.Size(), UpdatedAt: info.ModTime().UTC()}
		switch rep.Kind {
		case KindReview:
			id := strings.ToLower(strings.TrimSuffix(name, ".html"))
			rep.LibraryID = &id
			if n, ok := names[id]; ok {
				rep.Title = n
			}
		default:
			if t, ok := fixedTitles[name]; ok {
				rep.Title = t
			}
		}
		out = append(out, rep)
	}
	rank := map[string]int{KindPlan: 0, KindPreview: 1, KindAudit: 2, KindReview: 3, KindOther: 4}
	sort.Slice(out, func(i, j int) bool {
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"reports": out})
}

// Page implements GET /api/reports/{name}.
func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	name := chi.URLParam(r, "name")
	if !nameRe.MatchString(name) {
		httperror.Write(w, r, httperror.NotFound("report not found"))
		return
	}
	files, err := h.listing()
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list reports: "+err.Error()))
		return
	}
	info, ok := files[name]
	if !ok || !canSee(principal.FromContext(r.Context()), name) {
		httperror.Write(w, r, httperror.NotFound("report not found"))
		return
	}
	doc, err := os.ReadFile(filepath.Join(h.dir(), name))
	if err != nil {
		httperror.Write(w, r, httperror.NotFound("report not found"))
		return
	}
	// a page written before the reports learnt the theme rule still gets a
	// dark-or-system canvas (pagetheme: the app's theme outside the app)
	doc = pagetheme.Inject(doc)
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Security-Policy", pageCSP)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("X-Frame-Options", "SAMEORIGIN")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, name, info.ModTime(), bytes.NewReader(doc))
}

// Thumbnail implements GET /api/thumbnails/{hash}/small.jpg: the small copy
// a review page links by the relative path ../thumbnails/<hash>/small.jpg.
func (h *Handler) Thumbnail(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	hash := chi.URLParam(r, "hash")
	root := h.thumbDir()
	if root == "" || !hashRe.MatchString(hash) {
		httperror.Write(w, r, httperror.NotFound("thumbnail not found"))
		return
	}
	path := filepath.Join(root, hash, "small.jpg")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		httperror.Write(w, r, httperror.NotFound("thumbnail not found"))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		httperror.Write(w, r, httperror.NotFound("thumbnail not found"))
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "small.jpg", info.ModTime(), f)
}

func (h *Handler) previewer() pipeline.PlanPreviewer {
	p, _ := h.Pipeline.(pipeline.PlanPreviewer)
	return p
}

// PreviewStatus implements GET /api/reports/channel-plan/preview.
func (h *Handler) PreviewStatus(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	p := h.previewer()
	if p == nil {
		httperror.Write(w, r, httperror.BadGateway("the pipeline is not connected"))
		return
	}
	st, err := p.PlanPreviewStatus(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("pipeline: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, st)
}

// PreviewRequest is the POST body. Kind "preview" plans with the current
// settings and writes nothing; "stored" renders the latest stored plan.
type PreviewRequest struct {
	Kind string `json:"kind"`
}

// PreviewStart implements POST /api/reports/channel-plan/preview.
func (h *Handler) PreviewStart(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var body PreviewRequest
	if e := common.ReadJSON(r, &body, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if body.Kind == "" {
		body.Kind = "preview"
	}
	if body.Kind != "preview" && body.Kind != "stored" {
		httperror.Write(w, r, httperror.BadRequest(`kind must be "preview" or "stored"`))
		return
	}
	p := h.previewer()
	if p == nil {
		httperror.Write(w, r, httperror.BadGateway("the pipeline is not connected"))
		return
	}
	busy, err := p.PlanPreviewStart(r.Context(), body.Kind)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("pipeline: "+err.Error()))
		return
	}
	if busy {
		httperror.Write(w, r, httperror.Conflict("", "A preview is already being planned."))
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"started": true, "kind": body.Kind})
}
