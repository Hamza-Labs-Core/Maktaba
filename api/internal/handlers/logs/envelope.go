package logs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// Crash reports to the home (Epic 53 story 53.14): POST /api/diagnostics/envelope takes a
// Sentry envelope (README D5) from a signed-in device whose person turned crash reports on
// (the envelope's header carries consent: crash_reports_to_home; without it, 403). Its event
// is scrubbed again, kept as a file under <data>/logs/crashes/<yyyymmdd>/, and logged as one
// app_crashed error line, which the log index groups like any error (53.10).

const (
	// EnvelopeMax is the most one envelope may carry; EnvelopesPerHour the most a device
	// sends in an hour.
	EnvelopeMax      = 1 << 20
	EnvelopesPerHour = 50
	// ConsentCrashReports is the header's consent for crash reports to the home.
	ConsentCrashReports = "crash_reports_to_home"
)

// logsRoot is where the logs live: LogsRoot, else MAKTABA_DATA_DIR/logs ("": nowhere).
func (h *Handler) logsRoot() string {
	if h.LogsRoot != "" {
		return h.LogsRoot
	}
	if d := strings.TrimSpace(os.Getenv("MAKTABA_DATA_DIR")); d != "" {
		return filepath.Join(d, "logs")
	}
	return ""
}

// limiter counts each key's sends in a sliding hour (or day).
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *limiter) allow(key string, limit int, per time.Duration, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	keep := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < per {
			keep = append(keep, t)
		}
	}
	if len(keep) >= limit {
		l.hits[key] = keep
		return false, per - now.Sub(keep[0])
	}
	l.hits[key] = append(keep, now)
	return true, 0
}

var envelopes limiter

// envelopeHeader is the envelope's first line (the fields this home reads).
type envelopeHeader struct {
	EventID    string `json:"event_id"`
	Consent    string `json:"consent"`
	App        string `json:"app"`
	AppVersion string `json:"app_version"`
	Platform   string `json:"platform"`
	OSVersion  string `json:"os_version"`
	Device     string `json:"device_model"`
	InstallID  string `json:"install_id"`
}

type envItem struct {
	Type    string `json:"type"`
	Length  int    `json:"length"`
	payload []byte
}

// parseEnvelope reads a Sentry envelope: a header line, then for each item a header line
// and its payload (length bytes, or to the next newline without one). Unknown items are
// kept for the caller to skip, as the format asks.
func parseEnvelope(b []byte) (envelopeHeader, []envItem, error) {
	var hdr envelopeHeader
	r := bufio.NewReader(bytes.NewReader(b))
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return hdr, nil, fmt.Errorf("no header")
	}
	if err := json.Unmarshal(bytes.TrimSpace(line), &hdr); err != nil {
		return hdr, nil, fmt.Errorf("header: %w", err)
	}
	var items []envItem
	for {
		ih, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(ih)) == 0 {
			if err != nil {
				break
			}
			continue
		}
		var it envItem
		if err := json.Unmarshal(bytes.TrimSpace(ih), &it); err != nil {
			return hdr, items, fmt.Errorf("item header: %w", err)
		}
		if it.Length > 0 {
			it.payload = make([]byte, it.Length)
			if _, err := io.ReadFull(r, it.payload); err != nil {
				return hdr, items, fmt.Errorf("item %s: short", it.Type)
			}
			_, _ = r.ReadByte() // its newline
		} else {
			p, _ := r.ReadBytes('\n')
			it.payload = bytes.TrimRight(p, "\n")
		}
		items = append(items, it)
	}
	return hdr, items, nil
}

// scrubJSON scrubs every string of a decoded JSON value again (53.01's rules) and masks the
// values of sensitive keys.
func scrubJSON(v any) any {
	rules := mlog.SharedRedactRules()
	switch t := v.(type) {
	case string:
		return mlog.Scrub(t)
	case map[string]any:
		for k, x := range t {
			_, isStr := x.(string)
			if rules.KeySensitive(k, isStr) {
				t[k] = "***REDACTED***"
				continue
			}
			t[k] = scrubJSON(x)
		}
		return t
	case []any:
		for i := range t {
			t[i] = scrubJSON(t[i])
		}
		return t
	}
	return v
}

// crashEvent is what the line takes from a Sentry event.
type crashEvent struct {
	Exception struct {
		Values []struct {
			Type       string `json:"type"`
			Value      string `json:"value"`
			Stacktrace struct {
				Frames []struct {
					Function string `json:"function"`
					Module   string `json:"module"`
					InApp    bool   `json:"in_app"`
				} `json:"frames"`
			} `json:"stacktrace"`
		} `json:"values"`
	} `json:"exception"`
	Message string `json:"message"`
}

func (h *Handler) envelope(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context()) // RequireAuth in Mount
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, EnvelopeMax))
	if err != nil {
		httperror.Write(w, r, &httperror.Error{Type: "about:blank", Title: "the envelope is over 1 MB",
			Status: http.StatusRequestEntityTooLarge})
		return
	}
	hdr, items, err := parseEnvelope(body)
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("envelope: "+err.Error()))
		return
	}
	if hdr.Consent != ConsentCrashReports {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.tv/problems/crash-reports-off",
			Title: "crash reports to this home are off on this device", Status: http.StatusForbidden,
			Extras: map[string]any{"code": "crash_reports_off"}})
		return
	}
	device := p.UserID + "/" + hdr.InstallID
	if ok, wait := envelopes.allow(device, EnvelopesPerHour, time.Hour, time.Now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		httperror.Write(w, r, &httperror.Error{Type: "about:blank", Title: "too many crash reports from this device",
			Status: http.StatusTooManyRequests})
		return
	}
	var event map[string]any
	var attachment []byte
	for _, it := range items {
		switch it.Type {
		case "event":
			_ = json.Unmarshal(it.payload, &event)
		case "attachment":
			if len(it.payload) <= 512<<10 {
				attachment = []byte(mlog.Scrub(string(it.payload)))
			}
		}
	}
	if event == nil {
		httperror.Write(w, r, httperror.BadRequest("the envelope has no event"))
		return
	}
	event = scrubJSON(event).(map[string]any)
	id := uuid.NewString()
	userH := mlog.HashUser(p.UserID)
	if root := h.logsRoot(); root != "" {
		dir := filepath.Join(root, "crashes", time.Now().UTC().Format("20060102"))
		if err := os.MkdirAll(dir, 0o700); err == nil {
			doc := map[string]any{"id": id, "at": time.Now().UTC().Format(time.RFC3339), "user_h": userH,
				"install_id": hdr.InstallID, "app": hdr.App, "app_version": hdr.AppVersion, "platform": hdr.Platform,
				"os_version": hdr.OSVersion, "device_model": hdr.Device, "event": event, "log": string(attachment)}
			b, _ := json.Marshal(doc)
			_ = os.WriteFile(filepath.Join(dir, id+".json"), b, 0o600)
		}
	}
	// the line the index groups (53.10): the exception's type, value and first own frame
	var ce crashEvent
	b, _ := json.Marshal(event)
	_ = json.Unmarshal(b, &ce)
	code, msg, frame := "crash", ce.Message, ""
	if len(ce.Exception.Values) > 0 {
		ex := ce.Exception.Values[len(ce.Exception.Values)-1]
		code, msg = orStr(ex.Type, "crash"), orStr(ex.Value, msg)
		for _, f := range ex.Stacktrace.Frames {
			if f.InApp {
				frame = f.Function
			}
		}
	}
	stack := []any{}
	if frame != "" {
		stack = append(stack, map[string]any{"fn": frame, "own": true})
	}
	slog.Default().LogAttrs(r.Context(), slog.LevelError, "app_crashed",
		slog.String("crash_id", id), slog.String("app", hdr.App), slog.String("app_version", hdr.AppVersion),
		slog.String("platform", hdr.Platform), slog.String("install_id", hdr.InstallID), slog.String("user_h", userH),
		slog.Any("err", map[string]any{"kind": "crash", "code": code, "msg": msg, "stack": stack}),
		slog.String("event", "app_crashed"))
	common.WriteJSON(w, r, http.StatusOK, map[string]string{"id": id})
}

func orStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
