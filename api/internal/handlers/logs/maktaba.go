package logs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Problem reports to Maktaba (Epic 53 story 53.16). A report goes to Maktaba only when the
// home's admin turned on "Let people send problem reports to Maktaba" (the reports.share
// component, off by default, needing the cloud link) and the report was sent with the
// Maktaba box ticked, by its sender or later by an admin with their own preview. It leaves
// through the home's relay agent (POST /admin/cloud-link/reports, which adds the server's
// secret), after the export-only scrub (mlog.Export): no title, library path, name, email
// or address; its person is user-1. Until the cloud takes it, it waits in the report
// (maktaba.state queued) and the reports_forwarder loop tries again with backoff for 7 days.
//
//	GET    /api/reports/maktaba                any signed-in person: whether to show the box
//	PUT    /api/reports/maktaba                an admin: {"enabled"} (component.enable / .disable)
//	DELETE /api/reports/maktaba                an admin: delete every report Maktaba has
//	POST   /api/reports/{n}/share              an admin: send this report ({"screenshot"})
//	GET    /api/reports/{n}/maktaba-preview    an admin: the report exactly as it would leave

// ShareComponent is the component the sending is (catalog: reports.share).
const ShareComponent = "reports.share"

// Outbox timing.
const (
	shareFirstRetry = time.Minute
	shareMaxRetry   = 6 * time.Hour
	ShareKeepFor    = 7 * 24 * time.Hour
	repliesEvery    = 15 * time.Minute
)

// Where a report stands on its way to Maktaba.
const (
	ShareQueued    = "queued"
	ShareSent      = "sent"
	ShareExpired   = "expired"   // 7 days without the cloud
	ShareRefused   = "refused"   // the cloud said no (too large, not a report)
	ShareCancelled = "cancelled" // the admin turned sending off before it went
	ShareDeleted   = "deleted"   // sent, then deleted at Maktaba at the admin's request
)

// ToMaktaba is a report's way to Maktaba.
type ToMaktaba struct {
	State    string    `json:"state"`
	QueuedAt time.Time `json:"queued_at"`
	Attempts int       `json:"attempts,omitempty"`
	// NextAt is when a report that failed is tried again (zero: at once)
	NextAt     time.Time  `json:"next_at,omitempty"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
	CloudID    int64      `json:"cloud_id,omitempty"`
	Screenshot bool       `json:"screenshot"`
	Error      string     `json:"error,omitempty"`
	// Maktaba's answer
	Reply     string     `json:"reply,omitempty"`
	RepliedAt *time.Time `json:"replied_at,omitempty"`
	Status    string     `json:"status,omitempty"`
	FixedIn   string     `json:"fixed_in,omitempty"`
}

// Switches is what the reports read of the home's components.
type Switches interface {
	On(id string) bool
}

// maktabaState is the home's own record (<reports>/maktaba.json): where the replies were
// read up to, and the admin's request to delete what Maktaba has.
type maktabaState struct {
	RepliesSince    time.Time  `json:"replies_since"`
	RepliesReadAt   time.Time  `json:"replies_read_at"`
	DeleteRequested *time.Time `json:"delete_requested_at,omitempty"`
	Deleted         *time.Time `json:"deleted_at,omitempty"`
	DeleteError     string     `json:"delete_error,omitempty"`
}

func (h *Handler) shareOn() bool { return h.Switches != nil && h.Switches.On(ShareComponent) }

// linked is whether the home has a relay agent to send through (the cloud link is on).
func (h *Handler) linked() bool { return h.agentAddr() != "" }

func (h *Handler) agentAddr() string {
	if h.AgentAddr != "" {
		return h.AgentAddr
	}
	return os.Getenv("MAKTABA_CLOUDLINK_ADMIN")
}

func (h *Handler) readState() maktabaState {
	var st maktabaState
	if dir := h.reportsDir(); dir != "" {
		b, _ := os.ReadFile(filepath.Join(dir, "maktaba.json"))
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// updateState changes the home's record under the reports' lock.
func (h *Handler) updateState(fn func(*maktabaState)) error {
	dir := h.reportsDir()
	if dir == "" {
		return errors.New("this API keeps no reports")
	}
	reportsMu.Lock()
	defer reportsMu.Unlock()
	var st maktabaState
	b, _ := os.ReadFile(filepath.Join(dir, "maktaba.json"))
	_ = json.Unmarshal(b, &st)
	fn(&st)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	out, _ := json.Marshal(st)
	return os.WriteFile(filepath.Join(dir, "maktaba.json"), out, 0o600)
}

// updateReport reads, changes and writes report id under the reports' lock: the
// forwarder and the admin's routes change the same files.
func updateReport(dir string, id int, fn func(*Report) error) (Report, error) {
	reportsMu.Lock()
	defer reportsMu.Unlock()
	rep, own, err := readReport(dir, id)
	if err != nil {
		return rep, err
	}
	if err := fn(&rep); err != nil {
		return rep, err
	}
	return rep, writeReport(dir, rep, own, nil)
}

func reportIDs(dir string) []int {
	ents, _ := os.ReadDir(dir)
	var ids []int
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil {
			ids = append(ids, n)
		}
	}
	sort.Ints(ids)
	return ids
}

// --- the setting -------------------------------------------------------------------

func (h *Handler) maktabaSettings(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	out := map[string]any{"available": h.linked(), "enabled": h.shareOn() && h.linked()}
	if p.IsAdmin {
		st := h.readState()
		// a deletion that has not gone yet is tried again whenever an admin looks
		if st.DeleteRequested != nil && st.Deleted == nil && h.linked() {
			h.sendDelete(r.Context())
			st = h.readState()
		}
		del := map[string]any{"state": "none"}
		switch {
		case st.Deleted != nil && (st.DeleteRequested == nil || !st.Deleted.Before(*st.DeleteRequested)):
			del = map[string]any{"state": "done", "deleted_at": st.Deleted}
		case st.DeleteRequested != nil:
			del = map[string]any{"state": "pending", "requested_at": st.DeleteRequested, "error": st.DeleteError}
		}
		out["delete"] = del
		queued, sent := 0, 0
		if dir := h.reportsDir(); dir != "" {
			for _, id := range reportIDs(dir) {
				if rep, _, err := readReport(dir, id); err == nil && rep.Maktaba != nil {
					switch rep.Maktaba.State {
					case ShareQueued:
						queued++
					case ShareSent:
						sent++
					}
				}
			}
		}
		out["queued"], out["sent"] = queued, sent
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) setMaktaba(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil || in.Enabled == nil {
		httperror.Write(w, r, httperror.BadRequest(`{"enabled": true|false}`))
		return
	}
	if *in.Enabled && !h.linked() {
		httperror.Write(w, r, (&httperror.Error{Type: "about:blank", Title: "Link the home to Maktaba Cloud first",
			Status: http.StatusConflict}).With("code", "cloud_not_linked"))
		return
	}
	if h.DB == nil {
		httperror.Write(w, r, &httperror.Error{Type: "about:blank", Title: "this API has no database", Status: http.StatusServiceUnavailable})
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := components.Switch(ctx, components.New(h.DB), tx, ShareComponent, *in.Enabled, components.AdminAudit(ctx, tx)); err != nil {
		httperror.Write(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, err)
		return
	}
	if !*in.Enabled {
		// off stops new sends at once: what waits is not sent later
		h.cancelQueued()
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"enabled": *in.Enabled})
}

func (h *Handler) cancelQueued() {
	dir := h.reportsDir()
	if dir == "" {
		return
	}
	for _, id := range reportIDs(dir) {
		_, _ = updateReport(dir, id, func(rep *Report) error {
			if rep.Maktaba != nil && rep.Maktaba.State == ShareQueued {
				rep.Maktaba.State, rep.Maktaba.Error = ShareCancelled, ""
			}
			return nil
		})
	}
}

// deleteAtMaktaba is the admin's "Delete what Maktaba has": every report of the home, at
// once when the cloud answers, else as soon as it does (the forwarder, or the admin's next
// look at the setting).
func (h *Handler) deleteAtMaktaba(w http.ResponseWriter, r *http.Request) {
	if !h.linked() {
		httperror.Write(w, r, (&httperror.Error{Type: "about:blank", Title: "Link the home to Maktaba Cloud first",
			Status: http.StatusConflict}).With("code", "cloud_not_linked"))
		return
	}
	now := time.Now().UTC()
	if err := h.updateState(func(st *maktabaState) { st.DeleteRequested, st.DeleteError = &now, "" }); err != nil {
		httperror.Write(w, r, err)
		return
	}
	h.auditAs(r.Context(), principal.FromContext(r.Context()), "reports-delete-at-maktaba", "", nil)
	if h.sendDelete(r.Context()) {
		st := h.readState()
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"state": "done", "deleted_at": st.Deleted})
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"state": "pending"})
}

// sendDelete asks the cloud to delete the home's reports; true when it did.
func (h *Handler) sendDelete(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	code, _, err := h.agent(ctx, http.MethodDelete, "", nil, "")
	if err == nil && code/100 == 2 {
		now := time.Now().UTC()
		_ = h.updateState(func(st *maktabaState) { st.Deleted, st.DeleteError = &now, "" })
		if dir := h.reportsDir(); dir != "" {
			for _, id := range reportIDs(dir) {
				_, _ = updateReport(dir, id, func(rep *Report) error {
					if rep.Maktaba != nil && rep.Maktaba.State == ShareSent {
						rep.Maktaba.State = ShareDeleted
					}
					return nil
				})
			}
		}
		return true
	}
	msg := fmt.Sprintf("the cloud answered %d", code)
	if err != nil {
		msg = err.Error()
	}
	_ = h.updateState(func(st *maktabaState) { st.DeleteError = msg })
	return false
}

// --- sending -----------------------------------------------------------------------

// shareReport is an admin sending a report that was not sent: {"screenshot": false} drops
// its screenshot (the admin saw it in their own preview).
func (h *Handler) shareReport(w http.ResponseWriter, r *http.Request) {
	rep, _, ok := h.reportOf(w, r)
	if !ok {
		return
	}
	if !h.shareOn() || !h.linked() {
		httperror.Write(w, r, mw.ComponentOff(ShareComponent))
		return
	}
	var in struct {
		Screenshot bool `json:"screenshot"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
			httperror.Write(w, r, httperror.BadRequest(`{"screenshot": true|false}`))
			return
		}
	}
	now := time.Now().UTC()
	var conflict bool
	rep, err := updateReport(h.reportsDir(), rep.ID, func(x *Report) error {
		if x.Maktaba != nil && (x.Maktaba.State == ShareQueued || x.Maktaba.State == ShareSent) {
			conflict = true
			return nil
		}
		x.Shared = true
		x.Maktaba = &ToMaktaba{State: ShareQueued, QueuedAt: now, Screenshot: in.Screenshot && x.Screenshot}
		return nil
	})
	if err != nil {
		httperror.Write(w, r, err)
		return
	}
	if conflict {
		httperror.Write(w, r, (&httperror.Error{Type: "about:blank", Title: "This report is already on its way to Maktaba",
			Status: http.StatusConflict}).With("code", "already_shared"))
		return
	}
	h.auditAs(r.Context(), principal.FromContext(r.Context()), "report-share", strconv.Itoa(rep.ID),
		map[string]any{"screenshot": rep.Maktaba.Screenshot})
	h.Forwarder().Kick()
	common.WriteJSON(w, r, http.StatusAccepted, summary(rep))
}

// previewReport is the report exactly as it would leave the home (the admin's own preview).
func (h *Handler) previewReport(w http.ResponseWriter, r *http.Request) {
	rep, _, ok := h.reportOf(w, r)
	if !ok {
		return
	}
	body, err := h.exportReport(r.Context(), rep)
	if err != nil {
		httperror.Write(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// exportReport is the report's part that leaves the home: the export-only pass over every
// part, the sender's name removed, its person user-1.
func (h *Handler) exportReport(ctx context.Context, rep Report) ([]byte, error) {
	names := []string{rep.Name}
	rep.Name, rep.UserID, rep.Reply, rep.Status, rep.Maktaba = "", "", "", "", nil
	raw, err := json.Marshal(rep)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	for _, k := range []string{"name", "status", "reply", "shared_with_maktaba", "maktaba"} {
		delete(v, k)
	}
	e := mlog.NewExport(h.libraryRoots(ctx), names)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e.Value(v)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// libraryRoots are the home's library folders (a Postgres array, or JSON on SQLite).
func (h *Handler) libraryRoots(ctx context.Context) []string {
	if h.DB == nil {
		return nil
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT CAST(roots AS TEXT) FROM libraries`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s sql.NullString
		if rows.Scan(&s) == nil {
			out = append(out, parseRoots(s.String)...)
		}
	}
	return out
}

// parseRoots reads a Postgres TEXT[] literal ({a,"b c"}) or a JSON array.
func parseRoots(s string) []string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		var xs []string
		_ = json.Unmarshal([]byte(s), &xs)
		return xs
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
	var out []string
	var cur strings.Builder
	quoted, esc := false, false
	for _, c := range s {
		switch {
		case esc:
			cur.WriteRune(c)
			esc = false
		case c == '\\':
			esc = true
		case c == '"':
			quoted = !quoted
		case c == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// agent calls the relay agent's reports route: its answer's status and body.
func (h *Handler) agent(ctx context.Context, method, sub string, body io.Reader, ctype string) (int, []byte, error) {
	addr := h.agentAddr()
	if addr == "" {
		return 0, nil, errors.New("the home is not linked to Maktaba Cloud")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+"/admin/cloud-link/reports"+sub, body)
	if err != nil {
		return 0, nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	cl := h.ShareClient
	if cl == nil {
		cl = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, nil
}

// --- the loop ----------------------------------------------------------------------

// Forwarder is the api:reports_forwarder loop: it sends the queued reports, reads
// Maktaba's replies and passes a pending deletion. The runner runs it only while
// reports.share is on (and its cloud.link): off, nothing leaves.
type Forwarder struct {
	h    *Handler
	kick chan struct{}
	Log  *slog.Logger
	now  func() time.Time
}

// Forwarder is the handler's loop (one per handler).
func (h *Handler) Forwarder() *Forwarder {
	reportsMu.Lock()
	defer reportsMu.Unlock()
	if h.forwarder == nil {
		h.forwarder = &Forwarder{h: h, kick: make(chan struct{}, 1), now: time.Now}
	}
	return h.forwarder
}

// Kick runs a pass now (a report was queued).
func (f *Forwarder) Kick() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// Run passes every minute, and when kicked, until ctx ends.
func (f *Forwarder) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		f.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-f.kick:
		case <-t.C:
		}
	}
}

// Pass sends what is due, reads the replies when due, and passes a pending deletion.
func (f *Forwarder) Pass(ctx context.Context) {
	h := f.h
	dir := h.reportsDir()
	if dir == "" {
		return
	}
	now := f.now().UTC()
	st := h.readState()
	if st.DeleteRequested != nil && (st.Deleted == nil || st.Deleted.Before(*st.DeleteRequested)) {
		h.sendDelete(ctx)
	}
	for _, id := range reportIDs(dir) {
		if ctx.Err() != nil {
			return
		}
		rep, _, err := readReport(dir, id)
		if err != nil || rep.Maktaba == nil || rep.Maktaba.State != ShareQueued || rep.Maktaba.NextAt.After(now) {
			continue
		}
		f.send(ctx, dir, rep, now)
	}
	if now.Sub(st.RepliesReadAt) >= repliesEvery {
		f.readReplies(ctx, dir, st, now)
	}
}

func (f *Forwarder) send(ctx context.Context, dir string, rep Report, now time.Time) {
	h := f.h
	if now.Sub(rep.Maktaba.QueuedAt) > ShareKeepFor {
		_, _ = updateReport(dir, rep.ID, func(x *Report) error {
			if x.Maktaba != nil && x.Maktaba.State == ShareQueued {
				x.Maktaba.State = ShareExpired
			}
			return nil
		})
		f.log(slog.LevelWarn, "a problem report waited 7 days for Maktaba Cloud and was not sent", rep.ID, "problem_report_share_expired", nil)
		return
	}
	body, err := h.exportReport(ctx, rep)
	if err != nil {
		f.retry(dir, rep.ID, now, err.Error())
		return
	}
	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	_ = mp.WriteField("report", string(body))
	if rep.Maktaba.Screenshot {
		if shot, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(rep.ID), "screenshot.png")); err == nil {
			fw, _ := mp.CreateFormFile("screenshot", "screenshot.png")
			_, _ = fw.Write(shot)
		}
	}
	_ = mp.Close()
	sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	code, answer, err := h.agent(sctx, http.MethodPost, "", &buf, mp.FormDataContentType())
	cancel()
	switch {
	case err == nil && code/100 == 2:
		var got struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(answer, &got)
		_, _ = updateReport(dir, rep.ID, func(x *Report) error {
			if x.Maktaba != nil && x.Maktaba.State == ShareQueued {
				t := now
				x.Maktaba.State, x.Maktaba.SentAt, x.Maktaba.CloudID, x.Maktaba.Error = ShareSent, &t, got.ID, ""
			}
			return nil
		})
		f.log(slog.LevelInfo, "a problem report was sent to Maktaba", rep.ID, "problem_report_shared", nil)
	case err == nil && (code == http.StatusBadRequest || code == http.StatusRequestEntityTooLarge):
		_, _ = updateReport(dir, rep.ID, func(x *Report) error {
			if x.Maktaba != nil {
				x.Maktaba.State, x.Maktaba.Error = ShareRefused, fmt.Sprintf("Maktaba refused it (%d)", code)
			}
			return nil
		})
		f.log(slog.LevelWarn, "Maktaba Cloud refused a problem report", rep.ID, "problem_report_share_refused",
			map[string]any{"status": code})
	default:
		msg := fmt.Sprintf("the cloud answered %d", code)
		if err != nil {
			msg = err.Error()
		}
		f.retry(dir, rep.ID, now, msg)
	}
}

// retry backs a report off: 1 minute, doubling, at most 6 hours.
func (f *Forwarder) retry(dir string, id int, now time.Time, why string) {
	rep, _ := updateReport(dir, id, func(x *Report) error {
		if x.Maktaba == nil || x.Maktaba.State != ShareQueued {
			return nil
		}
		x.Maktaba.Attempts++
		wait := shareFirstRetry << min(x.Maktaba.Attempts-1, 20)
		x.Maktaba.NextAt, x.Maktaba.Error = now.Add(min(wait, shareMaxRetry)), why
		return nil
	})
	if rep.Maktaba != nil && rep.Maktaba.Attempts == 1 {
		f.log(slog.LevelWarn, "a problem report could not reach Maktaba Cloud; it waits and is tried again", id,
			"problem_report_share_waiting", map[string]any{"err": why})
	}
}

// readReplies brings Maktaba's answers into their reports.
func (f *Forwarder) readReplies(ctx context.Context, dir string, st maktabaState, now time.Time) {
	q := url.Values{}
	if !st.RepliesSince.IsZero() {
		q.Set("since", st.RepliesSince.UTC().Format(time.RFC3339Nano))
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	code, body, err := f.h.agent(rctx, http.MethodGet, "/replies?"+q.Encode(), nil, "")
	cancel()
	if err != nil || code != http.StatusOK {
		return
	}
	var got struct {
		Replies []struct {
			HomeReportID int       `json:"home_report_id"`
			Reply        string    `json:"reply"`
			RepliedAt    time.Time `json:"replied_at"`
			Status       string    `json:"status"`
			FixedIn      string    `json:"fixed_in"`
		} `json:"replies"`
	}
	if json.Unmarshal(body, &got) != nil {
		return
	}
	since := st.RepliesSince
	for _, r := range got.Replies {
		t := r.RepliedAt
		if t.After(since) {
			since = t
		}
		_, _ = updateReport(dir, r.HomeReportID, func(x *Report) error {
			if x.Maktaba == nil {
				return errors.New("not sent")
			}
			x.Maktaba.Reply, x.Maktaba.RepliedAt, x.Maktaba.Status, x.Maktaba.FixedIn = mlog.Scrub(r.Reply), &t, r.Status, r.FixedIn
			return nil
		})
	}
	_ = f.h.updateState(func(s *maktabaState) { s.RepliesSince, s.RepliesReadAt = since, now })
}

func (f *Forwarder) log(level slog.Level, msg string, id int, event string, extra map[string]any) {
	l := f.Log
	if l == nil {
		l = slog.Default()
	}
	attrs := []slog.Attr{slog.Int("report_id", id), slog.String("event", event)}
	for k, v := range extra {
		attrs = append(attrs, slog.Any(k, v))
	}
	l.LogAttrs(context.Background(), level, msg, attrs...)
}
