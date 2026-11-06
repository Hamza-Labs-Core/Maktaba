package recovery

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logkeeper"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Actions are what the console can do, given by serve (none deletes data).
type Actions struct {
	// RestartComponent restarts the processes of a catalog component, this boot only.
	RestartComponent func(id string) int
	// RestartAll restarts every role, requirements first.
	RestartAll func()
	// SafeMode writes (on) or removes the flag and restarts serve; stay writes it again.
	SafeMode func(on bool) error
	// Rollback is the previous version a committed update can go back to ("" when none),
	// and the call that starts it.
	Rollback func() (version string, do func() error)
	// Backups lists the backups on the data disk; Restore restores one (it replaces the
	// database: asked twice).
	Backups func() ([]Backup, error)
	Restore func(name, by string) error
}

// Backup is one restorable backup.
type Backup struct {
	Name string    `json:"name"`
	At   time.Time `json:"at"`
	Size int64     `json:"size"`
}

// Handler serves /recovery.
type Handler struct {
	Verifier *Verifier
	Codes    *Codes
	Sessions *Sessions
	Lockout  *Lockout
	Status   func() supervisor.StatusDoc
	// Processes is the supervisor's processes (its memory); Phases the reconciler's.
	Processes func() []supervisor.ChildStatus
	Phases    func() map[string]supervisor.ComponentPhase
	Catalog   *components.Catalog
	Keeper    *logkeeper.Keeper
	Actions   Actions
	// Disk is the data volume's free bytes and the log segments' size.
	Disk func() (free, logs int64)
	// Record writes an action to the supervisor's events, its log (recovery_action) and
	// the file the API copies into audit_log when it next starts.
	Record func(who Who, action, detail string)
	// Diagnostics adds the supervisor's own files to the bundle (events, the desired
	// state's cache, system info).
	Diagnostics func(add func(name string, body []byte))
	// Secure: the home serves HTTPS (the session cookie is Secure).
	Secure bool
	Now    func() time.Time
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// ServeHTTP routes /recovery and /recovery/api/*.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	lang := Lang(r.Header.Get("Accept-Language"))
	addr := clientAddr(r)
	if code := r.URL.Query().Get("code"); r.URL.Path == "/recovery" && code != "" {
		h.redeem(w, r, code, addr, lang)
		return
	}
	who, err := h.signIn(r, addr)
	if err != nil {
		h.refuse(w, r, lang, h.Lockout != nil && h.Lockout.Locked(addr))
		return
	}
	switch {
	case r.URL.Path == "/recovery" || r.URL.Path == "/recovery/":
		h.page(w, lang)
	case r.URL.Path == "/recovery/api/state" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, h.state())
	case r.URL.Path == "/recovery/api/logs" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, h.logs(r))
	case r.URL.Path == "/recovery/api/backups" && r.Method == http.MethodGet:
		h.backups(w)
	case r.URL.Path == "/recovery/api/diagnostics" && r.Method == http.MethodGet:
		h.Record(who, "diagnostics", "")
		h.diagnostics(w)
	case r.URL.Path == "/recovery/api/action" && r.Method == http.MethodPost:
		h.action(w, r, who)
	default:
		http.NotFound(w, r)
	}
}

// signIn: the console's session (a tray link), a pass cookie or a bearer token.
func (h *Handler) signIn(r *http.Request, addr string) (Who, error) {
	if h.Lockout != nil && h.Lockout.Locked(addr) {
		return Who{}, ErrDenied
	}
	if c, err := r.Cookie(CookieSession); err == nil && h.Sessions != nil && h.Sessions.Check(c.Value) {
		return Who{Tray: true}, nil
	}
	var tok string
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		tok = strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	} else if c, err := r.Cookie(CookiePass); err == nil {
		tok = c.Value
	}
	if tok == "" {
		// no credential at all is no failed try (a first visit)
		return Who{}, ErrDenied
	}
	who, err := h.Verifier.Verify(tok)
	if err != nil && h.Lockout != nil {
		h.Lockout.Fail(addr)
	}
	return who, err
}

func (h *Handler) redeem(w http.ResponseWriter, r *http.Request, code, addr, lang string) {
	if h.Lockout != nil && h.Lockout.Locked(addr) || h.Codes == nil || !h.Codes.Redeem(code) {
		if h.Lockout != nil {
			h.Lockout.Fail(addr)
		}
		h.refuse(w, r, lang, h.Lockout != nil && h.Lockout.Locked(addr))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieSession, Value: h.Sessions.Mint(), Path: "/recovery",
		MaxAge: int(SessionTTL.Seconds()), HttpOnly: true, Secure: h.Secure, SameSite: http.SameSiteStrictMode})
	h.Record(Who{Tray: true}, "sign_in", "tray link")
	http.Redirect(w, r, "/recovery", http.StatusSeeOther)
}

// refuse answers 401 with how to get in, and nothing about the home.
func (h *Handler) refuse(w http.ResponseWriter, r *http.Request, lang string, locked bool) {
	if strings.HasPrefix(r.URL.Path, "/recovery/api/") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", pageCSP)
	w.WriteHeader(http.StatusUnauthorized)
	msg := T(lang, "recovery.signin.denied") + " " + T(lang, "recovery.signin.mac") + " " + T(lang, "recovery.signin.later")
	if locked {
		msg = T(lang, "recovery.signin.locked")
	}
	_, _ = io.WriteString(w, signInPage(lang, msg))
}

// ComponentView is one component on the console.
type ComponentView struct {
	ID         string `json:"id"`
	Group      string `json:"group"`
	Phase      string `json:"phase,omitempty"`
	Reason     string `json:"reason,omitempty"`
	State      string `json:"state"`
	Restarts   int    `json:"restarts"`
	CutOff     bool   `json:"cut_off,omitempty"`
	LastExit   string `json:"last_exit,omitempty"`
	LastCode   *int   `json:"last_code,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	CanRestart bool   `json:"can_restart"`
}

// State is GET /recovery/api/state.
type State struct {
	Status     supervisor.StatusDoc `json:"status"`
	Components []ComponentView      `json:"components"`
	DiskFree   int64                `json:"disk_free"`
	LogsBytes  int64                `json:"logs_bytes"`
	Rollback   string               `json:"rollback,omitempty"`
	SafeMode   bool                 `json:"safe_mode"`
}

func (h *Handler) state() State {
	st := State{}
	if h.Status != nil {
		st.Status = h.Status()
	}
	st.SafeMode = st.Status.Mode == "safe"
	var phases map[string]supervisor.ComponentPhase
	if h.Phases != nil {
		phases = h.Phases()
	}
	byID := map[string]*ComponentView{}
	var procs []supervisor.ChildStatus
	if h.Processes != nil {
		procs = h.Processes()
	}
	for _, p := range procs {
		id := p.Component
		if id == "" {
			id = string(p.Role)
		}
		v := byID[id]
		if v == nil {
			v = &ComponentView{ID: id, Group: groupName(id), State: supervisor.StatusOK}
			byID[id] = v
		}
		v.Restarts += p.Restarts
		v.CutOff = v.CutOff || p.CutOff
		if s := procState(p); rank(s) > rank(v.State) {
			v.State = s
		}
		if p.LastExit != nil && (v.LastExit == "" || p.LastExit.At.Format(time.RFC3339) > v.LastExit) {
			v.LastExit = p.LastExit.At.UTC().Format(time.RFC3339)
			code := p.LastExit.Code
			v.LastCode = &code
		}
	}
	for id, ph := range phases {
		v := byID[id]
		if v == nil {
			continue
		}
		v.Phase, v.Reason = ph.Phase, ph.Reason
	}
	for _, v := range byID {
		off := v.Phase == components.PhaseOff || v.Phase == components.PhaseNotInstalled ||
			v.Phase == components.PhaseStopped || v.Phase == components.PhaseWaiting
		if off {
			v.State = supervisor.StatusOff
		}
		// a held or turned-off component is not restarted from here
		v.CanRestart = !off
		v.LastError = h.lastError(v.ID)
		st.Components = append(st.Components, *v)
	}
	sort.Slice(st.Components, func(i, j int) bool {
		if st.Components[i].Group != st.Components[j].Group {
			return groupOrder(st.Components[i].Group) < groupOrder(st.Components[j].Group)
		}
		return st.Components[i].ID < st.Components[j].ID
	})
	if h.Disk != nil {
		st.DiskFree, st.LogsBytes = h.Disk()
	}
	if h.Actions.Rollback != nil {
		st.Rollback, _ = h.Actions.Rollback()
	}
	return st
}

func procState(p supervisor.ChildStatus) string {
	switch p.State {
	case supervisor.StateRunning:
		return supervisor.StatusOK
	case supervisor.StateStarting:
		return supervisor.StatusStarting
	case supervisor.StateUnhealthy:
		return supervisor.StatusDegraded
	case supervisor.StateBackoff:
		return supervisor.StatusRestarting
	case supervisor.StateFailed:
		return supervisor.StatusDown
	}
	return supervisor.StatusOff
}

func rank(s string) int {
	return map[string]int{supervisor.StatusOff: 0, supervisor.StatusOK: 1, supervisor.StatusStarting: 2,
		supervisor.StatusDegraded: 3, supervisor.StatusRestarting: 4, supervisor.StatusDown: 5}[s]
}

func groupName(id string) string {
	for _, g := range supervisor.StatusGroups {
		for _, c := range g.Components {
			if c == id {
				return g.Name
			}
		}
	}
	return "other"
}

func groupOrder(g string) int {
	for i, sg := range supervisor.StatusGroups {
		if sg.Name == g {
			return i
		}
	}
	return len(supervisor.StatusGroups)
}

// lastError is the newest error line of a component in the last day (its segments).
func (h *Handler) lastError(id string) string {
	if h.Keeper == nil {
		return ""
	}
	res := h.Keeper.Read(logkeeper.Query{Component: id, MinLevel: "error", Since: h.now().Add(-24 * time.Hour), Limit: 1})
	if len(res.Lines) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(res.Lines[0], &m) != nil {
		return ""
	}
	msg, _ := m["msg"].(string)
	if attrs, ok := m["attrs"].(map[string]any); ok {
		if t, ok := attrs["text"].(string); ok && msg == "raw_line" {
			msg = t
		}
	}
	if e, ok := m["err"].(string); ok && e != "" {
		msg += ": " + e
	} else if e, ok := m["err"].(map[string]any); ok {
		if s, _ := e["msg"].(string); s != "" {
			msg += ": " + s
		}
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

// logs is GET /recovery/api/logs: warn and above of the last minutes (15 by default),
// by service, component and level, from the segments (no index needed).
func (h *Handler) logs(r *http.Request) logkeeper.Result {
	if h.Keeper == nil {
		return logkeeper.Result{}
	}
	q := r.URL.Query()
	minutes, _ := strconv.Atoi(q.Get("minutes"))
	if minutes <= 0 || minutes > 24*60 {
		minutes = 15
	}
	level := q.Get("level")
	if level == "" {
		level = "warn"
	}
	query := logkeeper.Query{MinLevel: level, Component: q.Get("component"), Text: q.Get("q"),
		Since: h.now().Add(-time.Duration(minutes) * time.Minute), Limit: 1000}
	if s := q.Get("service"); s != "" {
		query.Services = []string{s}
	}
	return h.Keeper.Read(query)
}

func (h *Handler) backups(w http.ResponseWriter) {
	if h.Actions.Backups == nil {
		writeJSON(w, http.StatusOK, map[string]any{"backups": []Backup{}})
		return
	}
	list, err := h.Actions.Backups()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": list})
}

// actionRequest is POST /recovery/api/action's body.
type actionRequest struct {
	Action    string `json:"action"`
	Component string `json:"component,omitempty"`
	Backup    string `json:"backup,omitempty"`
	// Confirm counts the person's confirmations: 1 for most actions, 2 for restore and
	// rollback, and for every action that comes through the relay.
	Confirm int `json:"confirm"`
}

func (h *Handler) action(w http.ResponseWriter, r *http.Request, who Who) {
	// a custom header no other site can send without a preflight: no cross-site action
	if r.Header.Get("X-Maktaba-Recovery") != "1" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing X-Maktaba-Recovery"})
		return
	}
	var req actionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	need := 1
	if req.Action == "restore" || req.Action == "rollback" || r.Header.Get("X-Maktaba-Via") != "" {
		need = 2
	}
	if req.Confirm < need {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{"error": "confirm", "confirm": need})
		return
	}
	var err error
	detail := req.Component
	switch req.Action {
	case "restart":
		var v *ComponentView
		for _, c := range h.state().Components {
			if c.ID == req.Component {
				c := c
				v = &c
			}
		}
		switch {
		case v == nil:
			err = errors.New("no such component here")
		case !v.CanRestart:
			err = errors.New("it is turned off or held: turn it on in Settings, Components")
		default:
			if h.Actions.RestartComponent(req.Component) == 0 && !v.CutOff {
				err = errors.New("none of its processes runs here now")
			}
		}
	case "restart_all":
		h.Actions.RestartAll()
	case "safe_mode", "stay_safe":
		err = h.Actions.SafeMode(true)
	case "leave_safe":
		err = h.Actions.SafeMode(false)
	case "rollback":
		if h.Actions.Rollback == nil {
			err = errors.New("no previous version is kept")
			break
		}
		v, do := h.Actions.Rollback()
		if v == "" || do == nil {
			err = errors.New("no previous version is kept")
			break
		}
		detail = v
		err = do()
	case "restore":
		if h.Actions.Restore == nil {
			err = errors.New("restore is not available here")
			break
		}
		detail = req.Backup
		by := "the recovery console"
		if who.UserID != "" {
			by += " (" + who.UserID + ")"
		}
		err = h.Actions.Restore(req.Backup, by)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no such action"})
		return
	}
	if err != nil {
		h.Record(who, req.Action, detail+": refused: "+err.Error())
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	h.Record(who, req.Action, detail)
	writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

// diagnostics is a .tar.gz of the last 2 hours of every service's segments, the status,
// the components, and the supervisor's own files; at most 50 MB of lines.
func (h *Handler) diagnostics(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="maktaba-recovery-%s.tar.gz"`,
		h.now().UTC().Format("20060102-150405")))
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	add := func(name string, body []byte) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), ModTime: h.now()})
		_, _ = tw.Write(body)
	}
	st, _ := json.MarshalIndent(h.state(), "", "  ")
	add("state.json", st)
	if h.Keeper != nil {
		res := h.Keeper.Read(logkeeper.Query{MinLevel: "debug", Since: h.now().Add(-2 * time.Hour), Limit: 200000})
		var b strings.Builder
		for i := len(res.Lines) - 1; i >= 0; i-- { // oldest first
			b.Write(res.Lines[i])
			b.WriteByte('\n')
			if b.Len() > 50<<20 {
				break
			}
		}
		add("logs.ndjson", []byte(b.String()))
	}
	if h.Diagnostics != nil {
		h.Diagnostics(add)
	}
	_ = tw.Close()
	_ = gz.Close()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// LinkHandler is the tray's one-time link, on the supervisor's loopback admin port:
// POST /recovery/link answers {"url": "<web>/recovery?code=..."}.
func LinkHandler(codes *Codes, webBase string, record func(Who, string, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST"})
			return
		}
		code := codes.Issue()
		if record != nil {
			record(Who{Tray: true}, "link", "")
		}
		writeJSON(w, http.StatusOK, map[string]any{"url": strings.TrimRight(webBase, "/") + "/recovery?code=" + code,
			"expires_in": int(CodeTTL.Seconds())})
	})
}
