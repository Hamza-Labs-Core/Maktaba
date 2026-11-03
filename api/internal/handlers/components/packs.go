package components

// The pack installer's routes (story 51.16), proxied to this machine's supervisor
// (its loopback /components/ routes behind the per-boot token):
//
//	POST /api/components/offline         {bundle?, components?, platform?}: build an offline file; 202 {name, url}
//	GET  /api/components/offline/{name}  202 {name, progress} while it builds, then the tar (Range works)
//	POST /api/components/import          the offline file as the body (a tar): install what it holds
//
// GET /api/components and /api/components/{id} carry each asset's install or removal
// progress from the same supervisor. Admin-only like every route of the package: the
// supervisor is never called for anyone else. Without a supervisor the routes answer 503
// components_unavailable and the lists carry no progress.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/serversettings"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Problem codes of the pack routes (the supervisor's reasons pass through as codes).
const (
	CodeUnavailable    = "components_unavailable"
	CodeOfflineUnknown = "offline_unknown"
	CodeImportNeedsTar = "import_needs_tar"
)

// maxImport is the largest offline file an import takes.
const maxImport = 64 << 30

// Packs is this machine's pack installer: its supervisor's loopback admin.
type Packs struct {
	Sup *serversettings.Supervisor
}

// PacksFromEnv is the supervisor this API runs under (nil: none).
func PacksFromEnv() *Packs {
	if s := serversettings.SupervisorFromEnv(); s != nil {
		return &Packs{Sup: s}
	}
	return nil
}

// stream is the supervisor without the 10 s timeout: an offline file or an import can
// take hours.
func (p *Packs) stream() *serversettings.Supervisor {
	s := *p.Sup
	c := &http.Client{}
	if p.Sup.HTTP != nil {
		c.Transport = p.Sup.HTTP.Transport
	}
	s.HTTP = c
	return &s
}

// SupError is a refusal of the supervisor: its status, its reason word and the rest.
type SupError struct {
	Status     int
	Reason     string
	Detail     string
	Params     map[string]any
	Installed  []string
	RequiredBy []string
}

func (e *SupError) Error() string {
	if e.Detail != "" {
		return e.Reason + ": " + e.Detail
	}
	return e.Reason
}

// supDoc is every answer of the supervisor's /components/ routes.
type supDoc struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Installed  json.RawMessage      `json:"installed"`
	Progress   *svc.InstallProgress `json:"progress"`
	Error      string               `json:"error"`
	Detail     string               `json:"detail"`
	Params     map[string]any       `json:"params"`
	Components []string             `json:"components"`
}

// read decodes an answer of the supervisor; a status of 300 or more is a *SupError.
func read(resp *http.Response) (supDoc, error) {
	var d supDoc
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return d, fmt.Errorf("the supervisor's answer: %w", err)
	}
	jerr := json.Unmarshal(raw, &d)
	if resp.StatusCode < 300 {
		if jerr != nil {
			return d, fmt.Errorf("the supervisor answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		return d, nil
	}
	e := &SupError{Status: resp.StatusCode, Reason: d.Error, Detail: d.Detail, Params: d.Params, RequiredBy: d.Components}
	_ = json.Unmarshal(d.Installed, &e.Installed)
	if e.Reason == "" {
		// a refusal with no word: the token (403) or a route this supervisor lacks
		e.Reason, e.Detail = CodeUnavailable, fmt.Sprintf("the supervisor answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return d, e
}

// Progress is id's last or running action on this machine (nil: none).
func (p *Packs) Progress(ctx context.Context, id string) (*svc.InstallProgress, error) {
	resp, err := p.Sup.Do(ctx, http.MethodGet, "/components/"+url.PathEscape(id)+"/progress", nil, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	d, err := read(resp)
	if err != nil || d.Progress == nil {
		return nil, err
	}
	_ = json.Unmarshal(d.Installed, &d.Progress.Installed)
	return d.Progress, nil
}

// ProgressOf is the progress of each of ids (left out: none, or no answer), asked at
// once, all within 3 s.
func (p *Packs) ProgressOf(ctx context.Context, ids []string) map[string]*svc.InstallProgress {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out := map[string]*svc.InstallProgress{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pr, err := p.Progress(ctx, id)
			if err != nil || pr == nil {
				return
			}
			mu.Lock()
			out[id] = pr
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// OfflineRequest is what an offline file holds: a bundle or components (none: the full
// set), for a platform ("": this machine's).
type OfflineRequest struct {
	Bundle     string   `json:"bundle,omitempty"`
	Components []string `json:"components,omitempty"`
	Platform   string   `json:"platform,omitempty"`
}

// MakeOffline asks the supervisor to build the offline file of req and answers its name.
func (p *Packs) MakeOffline(ctx context.Context, req OfflineRequest) (string, error) {
	b, _ := json.Marshal(req)
	resp, err := p.Sup.Do(ctx, http.MethodPost, "/components/offline", bytes.NewReader(b), "application/json")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	d, err := read(resp)
	if err != nil {
		return "", err
	}
	return d.Name, nil
}

// FetchOffline asks the supervisor for the offline file name. While it builds, the
// answer is its progress (pending, never nil) and no response; once built, the response
// (200, or 206 for a range) whose body is the tar: the caller closes it. header's Range
// and If-Range pass on. A build that failed, or a name it does not know, is a *SupError.
func (p *Packs) FetchOffline(ctx context.Context, name string, header http.Header) (*http.Response, *svc.InstallProgress, error) {
	s := p.stream()
	req, err := s.NewRequest(ctx, http.MethodGet, "/components/offline/"+url.PathEscape(name), nil)
	if err != nil {
		return nil, nil, err
	}
	for _, k := range []string{"Range", "If-Range"} {
		if v := header.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := s.Send(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
		return resp, nil, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, &SupError{Status: http.StatusNotFound, Reason: CodeOfflineUnknown,
			Detail: "no offline file of that name is built or kept on this machine"}
	}
	d, err := read(resp)
	switch {
	case err != nil:
		return nil, nil, err
	case resp.StatusCode != http.StatusAccepted:
		return nil, nil, &SupError{Status: http.StatusBadGateway, Reason: CodeUnavailable,
			Detail: fmt.Sprintf("the supervisor answered %d", resp.StatusCode)}
	case d.Progress == nil:
		d.Progress = &svc.InstallProgress{Phase: "offline", Busy: true}
	}
	return nil, d.Progress, nil
}

// Import hands the offline file in body (a tar) to the supervisor, which verifies and
// installs it, and answers the ids it installed. A refusal is a *SupError whose
// Installed lists what went in before it.
func (p *Packs) Import(ctx context.Context, body io.Reader, size int64) ([]string, error) {
	s := p.stream()
	req, err := s.NewRequest(ctx, http.MethodPost, "/components/import", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	if size > 0 {
		req.ContentLength = size
	}
	return importAnswer(s.Send(req))
}

// ImportPath asks the supervisor to import the offline file (or folder) at path on
// this machine: the CLI's way, where the file is beside the server.
func (p *Packs) ImportPath(ctx context.Context, path string) ([]string, error) {
	b, _ := json.Marshal(map[string]string{"path": path})
	return importAnswer(p.stream().Do(ctx, http.MethodPost, "/components/import", bytes.NewReader(b), "application/json"))
}

func importAnswer(resp *http.Response, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	d, err := read(resp)
	if err != nil {
		var se *SupError
		if errors.As(err, &se) {
			return se.Installed, err
		}
		return nil, err
	}
	var ids []string
	_ = json.Unmarshal(d.Installed, &ids)
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// WithProgress sets the progress of every asset of views from p (nil: none).
func WithProgress(ctx context.Context, p *Packs, views ...*svc.View) {
	if p == nil {
		return
	}
	var ids []string
	for _, v := range views {
		if v.Kind == components.KindAsset {
			ids = append(ids, v.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	got := p.ProgressOf(ctx, ids)
	for _, v := range views {
		v.Progress = got[v.ID]
	}
}

// packsFail writes a failure to reach or of the supervisor.
func (h *Handler) packsFail(w http.ResponseWriter, r *http.Request, err error) {
	var se *SupError
	if !errors.As(err, &se) {
		h.log().WarnContext(r.Context(), "components: the supervisor does not answer", "err", err, "event", "components_supervisor_unreachable")
		httperror.Write(w, r, problem(http.StatusServiceUnavailable, CodeUnavailable, "components unavailable",
			"this machine's maktaba-server does not answer"))
		return
	}
	status, code := se.Status, se.Reason
	switch {
	case code == "component_unknown":
		status, code = http.StatusNotFound, svc.CodeUnknown
	case code == CodeUnavailable:
		status = http.StatusServiceUnavailable
	case status == http.StatusNotFound:
		code = CodeOfflineUnknown
	case status >= 500:
		status = http.StatusBadGateway
	}
	e := problem(status, code, strings.ReplaceAll(code, "_", " "), se.Detail)
	if se.Params != nil {
		e = e.With("params", se.Params)
	}
	if se.Installed != nil {
		e = e.With("installed", se.Installed)
	}
	if se.RequiredBy != nil {
		e = e.With("required_by", se.RequiredBy)
	}
	httperror.Write(w, r, e)
}

func (h *Handler) noPacks(w http.ResponseWriter, r *http.Request) bool {
	if h.Packs != nil {
		return false
	}
	httperror.Write(w, r, problem(http.StatusServiceUnavailable, CodeUnavailable, "components unavailable",
		"this API runs under no maktaba-server supervisor"))
	return true
}

// record writes an audit row; a row that cannot be written is logged, never a failure
// of what was done.
func (h *Handler) record(r *http.Request, action, target string, payload map[string]any) {
	if err := h.Service.Record(r.Context(), h.actor(r), action, target, payload); err != nil {
		h.log().WarnContext(r.Context(), "components: an audit row was not written", "err", err, "action", action, "event", "components_audit_failed")
	}
}

func (h *Handler) offlineMake(w http.ResponseWriter, r *http.Request) {
	if h.noPacks(w, r) {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("body too large"))
		return
	}
	var req OfflineRequest
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			httperror.Write(w, r, httperror.BadRequest("body: "+err.Error()))
			return
		}
	}
	for _, id := range req.Components {
		if !h.Service.Known(id) {
			h.fail(w, r, svc.UnknownRefusal(id))
			return
		}
	}
	name, err := h.Packs.MakeOffline(r.Context(), req)
	if err != nil {
		h.packsFail(w, r, err)
		return
	}
	h.record(r, svc.ActionOfflineMake, "", map[string]any{"name": name, "bundle": req.Bundle,
		"components": req.Components, "platform": req.Platform})
	common.WriteJSON(w, r, http.StatusAccepted, map[string]string{"name": name, "url": "/api/components/offline/" + name})
}

// validOfflineName is a name the supervisor gives an offline file.
func validOfflineName(name string) bool {
	return strings.HasPrefix(name, "maktaba-offline-") && strings.HasSuffix(name, ".tar") &&
		!strings.ContainsAny(name, `/\`) && !strings.Contains(name, "..")
}

func (h *Handler) offlineGet(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !validOfflineName(name) {
		httperror.Write(w, r, problem(http.StatusNotFound, CodeOfflineUnknown, "no such offline file", "no offline file has that name"))
		return
	}
	if h.noPacks(w, r) {
		return
	}
	resp, pending, err := h.Packs.FetchOffline(r.Context(), name, r.Header)
	if err != nil {
		h.packsFail(w, r, err)
		return
	}
	if pending != nil {
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"name": name, "progress": pending})
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for _, k := range []string{"Content-Type", "Content-Disposition", "Content-Length", "Content-Range",
		"Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	h.record(r, svc.ActionOfflineDownload, "", map[string]any{"name": name, "range": r.Header.Get("Range")})
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// importFile streams the request body (the offline file, up to 64 GB) to the supervisor.
// What went in is recorded and, where the home wants it absent, becomes installed (off).
// ?name= is the file's name for the audit row.
func (h *Handler) importFile(w http.ResponseWriter, r *http.Request) {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "multipart/") {
		httperror.Write(w, r, problem(http.StatusUnsupportedMediaType, CodeImportNeedsTar, "send the file itself",
			"the request body is the offline file (application/x-tar)"))
		return
	}
	if r.ContentLength > maxImport {
		httperror.Write(w, r, problem(http.StatusRequestEntityTooLarge, "import_too_large", "file too large",
			"an offline file is at most 64 GB"))
		return
	}
	if h.noPacks(w, r) {
		return
	}
	file := r.URL.Query().Get("name")
	if len(file) > 200 {
		file = file[:200]
	}
	ids, err := h.Packs.Import(r.Context(), http.MaxBytesReader(w, r.Body, maxImport), r.ContentLength)
	res, rerr := h.imported(r, ids, file)
	if err != nil {
		h.packsFail(w, r, err)
		return
	}
	if rerr != nil {
		h.fail(w, r, rerr)
		return
	}
	if ids == nil {
		ids = []string{}
	}
	if res.Changed == nil {
		res.Changed = []store.Desired{}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"installed": ids, "changed": res.Changed})
}

// imported records ids (what an import installed, even one that then failed).
func (h *Handler) imported(r *http.Request, ids []string, file string) (svc.Result, error) {
	if len(ids) == 0 {
		return svc.Result{}, nil
	}
	// the files are on disk whatever the browser does now
	ctx := context.WithoutCancel(r.Context())
	return h.Service.Imported(ctx, ids, file, h.actor(r))
}
