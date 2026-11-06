package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// Error groups (Epic 53 story 53.10): the logs role groups this machine's error lines; the
// API lists them to admins, sets their status (each change in audit_log, category logs) and
// runs the notifier loop that tells admins of a new group or one that came back.

// AuditCategory is audit_log.category for the logs admin's actions.
const AuditCategory = "logs"

// EventErrorStatus is the audit action of a status change.
const EventErrorStatus = "error-group-status"

// errorStatus proxies the change to the role and, when it took, audits it.
func (h *Handler) errorStatus(w http.ResponseWriter, r *http.Request) {
	base := h.indexBase()
	if base == "" {
		h.indexDown(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("body too large"))
		return
	}
	fp := chi.URLParam(r, "fp")
	u := url.URL{Scheme: "http", Host: base, Path: "/errors/" + url.PathEscape(fp) + "/status"}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		httperror.Write(w, r, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		h.indexDown(w, r)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		var change map[string]any
		_ = json.Unmarshal(body, &change)
		h.audit(r.Context(), principal.FromContext(r.Context()), fp, change)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// audit writes the change to audit_log (best effort: the change is made either way).
func (h *Handler) audit(ctx context.Context, p *principal.Principal, fp string, change map[string]any) {
	if h.DB == nil {
		return
	}
	b, _ := json.Marshal(change)
	var actor any
	if p != nil {
		if _, err := uuid.Parse(p.UserID); err == nil {
			actor = p.UserID
		}
	}
	_, _ = h.DB.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
		VALUES ($1, $2, $3, $4, $5)`, AuditCategory, EventErrorStatus, actor, fp, string(b))
}

// NotifyKind is the notification kind of an error group (the web app's
// notify.logs.error_group and notify.logs.error_group_back, each with a _more form).
const NotifyKind = "logs.error_group"

// ErrorNotifier asks the logs role every minute which groups admins should hear about
// (new with 3 lines in 10 minutes, or back on a later version; once a group a day, at most
// 10 a day, the rest as "and N more") and notifies them. It is the loop api:errors_notifier
// of the catalog's logs.index: off with it.
type ErrorNotifier struct {
	Addr     func() string
	Notifier notify.Notifier
	Logger   *slog.Logger
	Every    time.Duration
	Client   *http.Client
	// DailyLimit is the most notices a day (0: 10).
	DailyLimit int
}

// NewErrorNotifier is the loop over this API's logs role.
func NewErrorNotifier(n notify.Notifier, logger *slog.Logger) *ErrorNotifier {
	h := &Handler{}
	return &ErrorNotifier{Addr: h.indexBase, Notifier: n, Logger: logger}
}

// Run polls until ctx ends.
func (e *ErrorNotifier) Run(ctx context.Context) {
	every := e.Every
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		e.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type notice struct {
	FP         string `json:"fp"`
	Title      string `json:"title"`
	Component  string `json:"component"`
	Service    string `json:"service"`
	CameBackIn string `json:"came_back_in"`
}

// Once is one poll.
func (e *ErrorNotifier) Once(ctx context.Context) {
	base := ""
	if e.Addr != nil {
		base = e.Addr()
	}
	if base == "" || e.Notifier == nil {
		return
	}
	limit := e.DailyLimit
	if limit <= 0 {
		limit = 10
	}
	c := e.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+base+"/errors/notices?limit="+strconv.Itoa(limit), nil)
	if err != nil {
		return
	}
	resp, err := c.Do(req)
	if err != nil {
		return // the role is restarting; the next minute asks again
	}
	defer resp.Body.Close()
	var out struct {
		Notices []notice `json:"notices"`
		More    int      `json:"more"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil {
		return
	}
	for i, n := range out.Notices {
		key := "notify.logs.error_group"
		if n.CameBackIn != "" {
			key = "notify.logs.error_group_back"
		}
		params := map[string]any{"title": n.Title, "component": n.Component, "service": n.Service,
			"version": n.CameBackIn}
		if i == len(out.Notices)-1 && out.More > 0 {
			params["more"] = out.More
			key += "_more" // "… and N more today"
		}
		if err := e.Notifier.Notify(ctx, NotifyKind, notify.Payload{Severity: notify.SeverityError, TitleKey: key,
			Params: params, Link: "/admin/errors/" + n.FP, Subject: "error_group:" + n.FP, Every: 24 * time.Hour}); err != nil &&
			e.Logger != nil {
			e.Logger.Warn("error group notice not sent", "event", "error_notice_failed", "err", err.Error())
		}
	}
}
