package logs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Report a problem (Epic 53 story 53.15). Any signed-in person sends one from an app:
// POST /api/reports, a multipart form with the app's part ("report", JSON: what happened,
// its category and time, the device, the app's recent lines and request ids, its crash
// reports) and a screenshot only when the person ticked it and saw it (the report says
// screenshot_previewed; a screenshot without it is refused). The home adds its side: every
// server line of the report's requests and the warn and error lines of every part around its
// time, from the log index. The report is kept under <data>/logs/reports/<n>/ (90 days by
// default: the logs role's sweep), and the admins are notified.
//
//	GET  /api/reports              the admin's inbox, or a viewer's own reports
//	GET  /api/reports/{n}          an admin: the whole report; its sender: status and reply
//	POST /api/reports/{n}/status   an admin: new, looking, fixed or closed
//	POST /api/reports/{n}/reply    an admin: a short message its sender sees
//	GET  /api/reports/{n}/bundle   an admin: the report as one .tar.gz

const (
	ReportPartMax        = 512 << 10
	ScreenshotMax        = 1 << 20
	ReportsPerDay        = 10
	ReportTextMax        = 2000
	EventProblemReported = "problem_reported"
	NotifyReportKind     = "logs.problem_report"
)

var reportCategories = map[string]bool{"playback": true, "live_tv": true, "search": true, "signing_in": true, "other": true}
var reportStatuses = map[string]bool{"new": true, "looking": true, "fixed": true, "closed": true}

// Report is one problem report.
type Report struct {
	ID         int       `json:"id"`
	At         time.Time `json:"at"`
	When       time.Time `json:"when"` // the time the problem happened ("just now": At)
	UserH      string    `json:"user_h"`
	UserID     string    `json:"-"`
	Name       string    `json:"name,omitempty"`
	App        string    `json:"app"`
	Version    string    `json:"version"`
	Platform   string    `json:"platform"`
	OSVersion  string    `json:"os_version,omitempty"`
	Device     string    `json:"device_model,omitempty"`
	Category   string    `json:"category"`
	Text       string    `json:"text,omitempty"`
	Status     string    `json:"status"`
	Reply      string    `json:"reply,omitempty"`
	Shared     bool      `json:"shared_with_maktaba"`
	Screenshot bool      `json:"screenshot"`
	// the app's part
	Requests []string          `json:"request_ids,omitempty"`
	Lines    []json.RawMessage `json:"lines,omitempty"`
	Crashes  []json.RawMessage `json:"crashes,omitempty"`
	// the home's part
	Server []json.RawMessage `json:"server,omitempty"`
}

// reportOwner is the meta kept beside a report for who may read it.
type reportOwner struct {
	UserID string `json:"user_id"`
}

var (
	reportsMu sync.Mutex
	reportsRL limiter
	// reportNotifier is the notifications hub (SetReportNotifier; nil: none)
	reportNotifier notify.Notifier
)

// ReportNotifier is the hub new reports notify through (nil: none).
func ReportNotifier() notify.Notifier {
	reportsMu.Lock()
	defer reportsMu.Unlock()
	return reportNotifier
}

// SetReportNotifier sets the hub new reports notify admins through (main, once).
func SetReportNotifier(n notify.Notifier) {
	reportsMu.Lock()
	reportNotifier = n
	reportsMu.Unlock()
}

func (h *Handler) reportsDir() string {
	if root := h.logsRoot(); root != "" {
		return filepath.Join(root, "reports")
	}
	return ""
}

type reportIn struct {
	Category            string            `json:"category"`
	Text                string            `json:"text"`
	When                string            `json:"when"`
	App                 string            `json:"app"`
	Version             string            `json:"version"`
	Platform            string            `json:"platform"`
	OSVersion           string            `json:"os_version"`
	Device              string            `json:"device_model"`
	InstallID           string            `json:"install_id"`
	Requests            []string          `json:"request_ids"`
	Lines               []json.RawMessage `json:"lines"`
	Crashes             []json.RawMessage `json:"crashes"`
	ScreenshotPreviewed bool              `json:"screenshot_previewed"`
	ShareWithMaktaba    bool              `json:"share_with_maktaba"`
}

func (h *Handler) createReport(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	dir := h.reportsDir()
	if dir == "" {
		httperror.Write(w, r, &httperror.Error{Type: "about:blank", Title: "this API keeps no reports", Status: http.StatusConflict})
		return
	}
	if err := r.ParseMultipartForm(ReportPartMax + ScreenshotMax + 64<<10); err != nil {
		httperror.Write(w, r, httperror.BadRequest("the report: "+err.Error()))
		return
	}
	raw := r.FormValue("report")
	if raw == "" {
		if f, _, err := r.FormFile("report"); err == nil {
			b, _ := io.ReadAll(io.LimitReader(f, ReportPartMax+1))
			_ = f.Close()
			raw = string(b)
		}
	}
	if len(raw) > ReportPartMax {
		httperror.Write(w, r, &httperror.Error{Type: "about:blank", Title: "the app's part is over 512 KB", Status: http.StatusRequestEntityTooLarge})
		return
	}
	var in reportIn
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		httperror.Write(w, r, httperror.BadRequest("the report: "+err.Error()))
		return
	}
	if !reportCategories[in.Category] {
		httperror.Write(w, r, httperror.BadRequest("category: playback, live_tv, search, signing_in or other"))
		return
	}
	if len([]rune(in.Text)) > ReportTextMax {
		httperror.Write(w, r, httperror.BadRequest("what happened: at most 2,000 characters"))
		return
	}
	var shot []byte
	if f, fh, err := r.FormFile("screenshot"); err == nil {
		defer f.Close()
		if !in.ScreenshotPreviewed {
			// a screenshot is sent only after the person saw it in the preview
			httperror.Write(w, r, httperror.BadRequest("a screenshot must be previewed before it is sent"))
			return
		}
		if fh.Size > ScreenshotMax {
			httperror.Write(w, r, &httperror.Error{Type: "about:blank", Title: "the screenshot is over 1 MB", Status: http.StatusRequestEntityTooLarge})
			return
		}
		shot, _ = io.ReadAll(io.LimitReader(f, ScreenshotMax))
	}
	if ok, wait := reportsRL.allow(p.UserID+"/"+in.InstallID, ReportsPerDay, 24*time.Hour, time.Now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		httperror.Write(w, r, &httperror.Error{Type: "about:blank", Title: "too many reports from this device today", Status: http.StatusTooManyRequests})
		return
	}
	now := time.Now().UTC()
	when := now
	if t, err := time.Parse(time.RFC3339, in.When); err == nil && t.Before(now) && now.Sub(t) < 24*time.Hour {
		when = t.UTC()
	}
	rep := Report{At: now, When: when, UserH: mlog.HashUser(p.UserID), App: in.App, Version: in.Version,
		Platform: in.Platform, OSVersion: in.OSVersion, Device: in.Device, Category: in.Category,
		Text: mlog.Scrub(in.Text), Status: "new", Shared: in.ShareWithMaktaba, Screenshot: shot != nil}
	for _, rid := range in.Requests {
		if len(rep.Requests) < 20 && len(rid) <= 64 {
			rep.Requests = append(rep.Requests, rid)
		}
	}
	for i, l := range in.Lines {
		if i >= 2000 {
			break
		}
		rep.Lines = append(rep.Lines, scrubRaw(l))
	}
	for i, c := range in.Crashes {
		if i >= 5 {
			break
		}
		rep.Crashes = append(rep.Crashes, scrubRaw(c))
	}
	rep.Server = h.homeSide(r.Context(), rep)
	rep.Server = withoutPluginLines(rep.Server, h.reportPlugins(r.Context()))
	if h.DB != nil {
		_ = h.DB.QueryRowContext(r.Context(), `SELECT username FROM users WHERE id = $1`, p.UserID).Scan(&rep.Name)
	}
	reportsMu.Lock()
	id, err := nextReportID(dir)
	if err == nil {
		rep.ID = id
		err = writeReport(dir, rep, reportOwner{UserID: p.UserID}, shot)
	}
	n := reportNotifier
	reportsMu.Unlock()
	if err != nil {
		httperror.Write(w, r, err)
		return
	}
	slog.Default().LogAttrs(r.Context(), slog.LevelInfo, EventProblemReported, slog.Int("report_id", rep.ID),
		slog.String("category", rep.Category), slog.String("app", rep.App), slog.String("event", EventProblemReported))
	if n != nil {
		_ = n.Notify(r.Context(), NotifyReportKind, notify.Payload{Severity: notify.SeverityWarn,
			TitleKey: "notify.logs.problem_report", Params: map[string]any{"name": rep.Name, "category": rep.Category,
				"id": rep.ID}, Link: "/admin/reports/" + strconv.Itoa(rep.ID), Subject: "report:" + strconv.Itoa(rep.ID),
			Every: -1})
	}
	common.WriteJSON(w, r, http.StatusCreated, map[string]any{"id": rep.ID})
}

func scrubRaw(b json.RawMessage) json.RawMessage {
	var v any
	if json.Unmarshal(b, &v) != nil {
		s, _ := json.Marshal(mlog.Scrub(string(b)))
		return s
	}
	out, _ := json.Marshal(scrubJSON(v))
	return out
}

// homeSide is the server's lines for a report: every line of its requests, and the warn
// and error lines of every part from 10 minutes before its time to 2 after (at most 2,000).
func (h *Handler) homeSide(ctx context.Context, rep Report) []json.RawMessage {
	base := h.indexBase()
	if base == "" {
		return nil
	}
	c := &http.Client{Timeout: 5 * time.Second}
	get := func(q url.Values) []json.RawMessage {
		u := url.URL{Scheme: "http", Host: base, Path: "/search", RawQuery: q.Encode()}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		var res struct {
			Lines []json.RawMessage `json:"lines"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&res)
		return res.Lines
	}
	var out []json.RawMessage
	seen := map[string]bool{}
	add := func(ls []json.RawMessage) {
		for _, l := range ls {
			if len(out) < 2000 && !seen[string(l)] {
				seen[string(l)] = true
				out = append(out, l)
			}
		}
	}
	for _, rid := range rep.Requests {
		add(get(url.Values{"request_id": {rid}, "limit": {"200"}}))
	}
	add(get(url.Values{"level": {"warn"}, "since": {rep.When.Add(-10 * time.Minute).Format(time.RFC3339)},
		"until": {rep.When.Add(2 * time.Minute).Format(time.RFC3339)}, "limit": {"500"}}))
	return out
}

func nextReportID(dir string) (int, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	last := 0
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil && n > last {
			last = n
		}
	}
	// the counter survives a report the sweep removed
	if b, err := os.ReadFile(filepath.Join(dir, ".last")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > last {
			last = n
		}
	}
	next := last + 1
	return next, os.WriteFile(filepath.Join(dir, ".last"), []byte(strconv.Itoa(next)), 0o600)
}

func writeReport(dir string, rep Report, owner reportOwner, shot []byte) error {
	d := filepath.Join(dir, strconv.Itoa(rep.ID))
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(rep)
	if err := os.WriteFile(filepath.Join(d, "report.json"), b, 0o600); err != nil {
		return err
	}
	ob, _ := json.Marshal(owner)
	if err := os.WriteFile(filepath.Join(d, "owner.json"), ob, 0o600); err != nil {
		return err
	}
	if shot != nil {
		return os.WriteFile(filepath.Join(d, "screenshot.png"), shot, 0o600)
	}
	return nil
}

func readReport(dir string, id int) (Report, reportOwner, error) {
	d := filepath.Join(dir, strconv.Itoa(id))
	var rep Report
	var own reportOwner
	b, err := os.ReadFile(filepath.Join(d, "report.json"))
	if err != nil {
		return rep, own, err
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return rep, own, err
	}
	ob, _ := os.ReadFile(filepath.Join(d, "owner.json"))
	_ = json.Unmarshal(ob, &own)
	return rep, own, nil
}

// summary is a report as a list shows it.
func summary(rep Report) map[string]any {
	return map[string]any{"id": rep.ID, "at": rep.At, "name": rep.Name, "app": rep.App, "category": rep.Category,
		"status": rep.Status, "shared_with_maktaba": rep.Shared, "reply": rep.Reply}
}

func (h *Handler) listReports(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	dir := h.reportsDir()
	out := []map[string]any{}
	if dir != "" {
		ents, _ := os.ReadDir(dir)
		var ids []int
		for _, e := range ents {
			if n, err := strconv.Atoi(e.Name()); err == nil {
				ids = append(ids, n)
			}
		}
		sort.Sort(sort.Reverse(sort.IntSlice(ids)))
		for _, id := range ids {
			rep, own, err := readReport(dir, id)
			if err != nil || (!p.IsAdmin && own.UserID != p.UserID) {
				continue
			}
			out = append(out, summary(rep))
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"reports": out})
}

func (h *Handler) reportOf(w http.ResponseWriter, r *http.Request) (Report, reportOwner, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	dir := h.reportsDir()
	if err != nil || dir == "" {
		httperror.Write(w, r, httperror.NotFound("no such report"))
		return Report{}, reportOwner{}, false
	}
	rep, own, err := readReport(dir, id)
	if errors.Is(err, os.ErrNotExist) {
		httperror.Write(w, r, httperror.NotFound("no such report"))
		return rep, own, false
	}
	if err != nil {
		httperror.Write(w, r, err)
		return rep, own, false
	}
	return rep, own, true
}

func (h *Handler) getReport(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	rep, own, ok := h.reportOf(w, r)
	if !ok {
		return
	}
	switch {
	case p.IsAdmin:
		common.WriteJSON(w, r, http.StatusOK, rep)
	case own.UserID == p.UserID:
		// its sender sees what they sent, its status and the reply; never the server's part
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"id": rep.ID, "at": rep.At, "category": rep.Category,
			"text": rep.Text, "status": rep.Status, "reply": rep.Reply, "app": rep.App})
	default:
		httperror.Write(w, r, httperror.NotFound("no such report"))
	}
}

func (h *Handler) changeReport(field string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, own, ok := h.reportOf(w, r)
		if !ok {
			return
		}
		var body struct {
			Status string `json:"status"`
			Reply  string `json:"reply"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
			httperror.Write(w, r, httperror.BadRequest("body"))
			return
		}
		switch field {
		case "status":
			if !reportStatuses[body.Status] {
				httperror.Write(w, r, httperror.BadRequest("status: new, looking, fixed or closed"))
				return
			}
			rep.Status = body.Status
		case "reply":
			if len([]rune(body.Reply)) > 1000 {
				httperror.Write(w, r, httperror.BadRequest("reply: at most 1,000 characters"))
				return
			}
			rep.Reply = body.Reply
		}
		reportsMu.Lock()
		err := writeReport(h.reportsDir(), rep, own, nil)
		reportsMu.Unlock()
		if err != nil {
			httperror.Write(w, r, err)
			return
		}
		h.auditAs(r.Context(), principal.FromContext(r.Context()), "report-"+field, strconv.Itoa(rep.ID),
			map[string]any{field: body.Status + body.Reply})
		common.WriteJSON(w, r, http.StatusOK, summary(rep))
	}
}

func (h *Handler) reportBundle(w http.ResponseWriter, r *http.Request) {
	rep, _, ok := h.reportOf(w, r)
	if !ok {
		return
	}
	d := filepath.Join(h.reportsDir(), strconv.Itoa(rep.ID))
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"report.json", "screenshot.png"} {
		b, err := os.ReadFile(filepath.Join(d, name))
		if err != nil {
			continue
		}
		_ = tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("report-%d/%s", rep.ID, name), Mode: 0o600, Size: int64(len(b)),
			ModTime: rep.At})
		_, _ = tw.Write(b)
	}
	_ = tw.Close()
	_ = gz.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="maktaba-report-%d.tar.gz"`, rep.ID))
	_, _ = w.Write(buf.Bytes())
}

// PluginReportLogsKey is the app setting naming the plugins whose lines a report may
// carry (a JSON list of plugin ids; plugins.ReportLogsKey, written from each plugin's page).
const PluginReportLogsKey = "plugins_report_logs"

// reportPlugins are the plugins an admin allowed in problem reports (story 54.09 §3).
func (h *Handler) reportPlugins(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if h.DB == nil {
		return out
	}
	var raw sql.NullString
	if h.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, PluginReportLogsKey).Scan(&raw) != nil || !raw.Valid {
		return out
	}
	var ids []string
	_ = json.Unmarshal([]byte(raw.String), &ids)
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// withoutPluginLines leaves out the lines of every plugin not allowed in reports (story
// 54.09 §3, README §9 T2): a plugin's lines are its own text, which a report sends to
// whoever reads it. The host's own lines (plugins.host) stay.
func withoutPluginLines(lines []json.RawMessage, allowed map[string]bool) []json.RawMessage {
	out := lines[:0:0]
	for _, l := range lines {
		var head struct {
			Service string `json:"service"`
			Origin  string `json:"origin"`
		}
		_ = json.Unmarshal(l, &head)
		if id, ok := strings.CutPrefix(head.Service, "plugin."); ok && !allowed[id] {
			continue
		}
		if head.Origin == "plugin" && !strings.HasPrefix(head.Service, "plugin.") {
			continue // a plugin's line under another name: never carried
		}
		out = append(out, l)
	}
	return out
}
