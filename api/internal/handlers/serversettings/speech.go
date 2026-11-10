package serversettings

// Speech to text on this machine (Epic 59 story 59.06), for the web's Settings → This
// machine:
//
//	GET  /api/server/speech          the supervisor's /config/speech (on or off, the model,
//	                                 why the tier picked it, the sizes offered, the last
//	                                 measure of this machine's speed) and, from the database,
//	                                 the backlog: the audio waiting for transcription, how
//	                                 long it would take at the measured speed, and when that
//	                                 is over 30 days the next smaller model's estimate (or
//	                                 speech off, when none is smaller)
//	PUT  /api/server/speech          {"speech": "on"|"off"|"auto", "model": "<size>"|"auto"}
//	                                 for the next start
//	POST /api/server/speech/measure  measure this machine's speed again now
//
// They proxy the supervisor. Admins only (403 before the supervisor is called); a change
// and a measure asked for are audited (category 'server').

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// The audit actions of the speech routes.
const (
	ActionSpeechChanged  = "speech-changed"
	ActionSpeechMeasured = "speech-measure"
)

// SpeechSuggestDays is the backlog's estimate above which a smaller model (or speech off)
// is suggested.
const SpeechSuggestDays = 30

func (h *Handler) mountSpeech(r chi.Router) {
	r.Get("/api/server/speech", h.admin(h.speechGet))
	r.Put("/api/server/speech", h.admin(h.speechPut))
	r.Post("/api/server/speech/measure", h.admin(h.speechMeasure))
}

// SpeechModelCost is a size as the supervisor describes it: its name and relative CPU cost.
type SpeechModelCost struct {
	Name string  `json:"name"`
	Cost float64 `json:"cost"`
}

// speechView is the part of the supervisor's answer the estimate reads.
type speechView struct {
	Speech struct {
		On    bool   `json:"on"`
		Model string `json:"model"`
		Repo  string `json:"repo"`
	} `json:"speech"`
	Measure *struct {
		Model string  `json:"model"`
		RTF   float64 `json:"rtf"`
		Error string  `json:"error"`
	} `json:"measure"`
	QuietHours string        `json:"quiet_hours"`
	Models     []SpeechModelCost `json:"models"`
}

// SpeechBacklog is the audio waiting for transcription.
type SpeechBacklog struct {
	Files int     `json:"files"`
	Hours float64 `json:"hours"`
}

// SpeechEstimate is how long the backlog takes with one model: Days at RTF (audio seconds
// per wall second; Measured: this machine's own measure, else scaled from it by the
// models' costs), working HoursPerDay (the quiet window's length when there is one: a
// backlog waits for it).
type SpeechEstimate struct {
	Model       string  `json:"model"`
	RTF         float64 `json:"rtf"`
	Measured    bool    `json:"measured"`
	HoursPerDay float64 `json:"hours_per_day"`
	Days        float64 `json:"days"`
}

// SpeechAdvice is the backlog's estimate with the running model and, when it is over
// SpeechSuggestDays, the next smaller model's (Suggest) or speech off (Off: none is smaller).
type SpeechAdvice struct {
	Backlog  *SpeechBacklog  `json:"backlog"`
	Estimate *SpeechEstimate `json:"estimate"`
	Suggest  *SpeechEstimate `json:"suggest,omitempty"`
	Off      bool            `json:"suggest_off,omitempty"`
}

// QuietHoursLength is the length of a window "01:00-06:00" in hours (24: none, or one that
// does not parse).
func QuietHoursLength(w string) float64 {
	a, b, ok := strings.Cut(strings.TrimSpace(w), "-")
	if !ok {
		return 24
	}
	clock := func(s string) (int, bool) {
		hh, mm, ok := strings.Cut(strings.TrimSpace(s), ":")
		h, e1 := strconv.Atoi(hh)
		m, e2 := strconv.Atoi(mm)
		return h*60 + m, ok && e1 == nil && e2 == nil && h >= 0 && h < 24 && m >= 0 && m < 60
	}
	start, ok1 := clock(a)
	end, ok2 := clock(b)
	if !ok1 || !ok2 || start == end {
		return 24
	}
	mins := end - start
	if mins < 0 {
		mins += 24 * 60
	}
	return float64(mins) / 60
}

// Advise is the estimate for backlog at the measured rtf of model, working in quiet (the
// window, "" none), and when over SpeechSuggestDays the next smaller of models (fastest
// first by cost) or speech off. Nil estimate: no measure of model.
func Advise(backlog SpeechBacklog, model string, rtf float64, quiet string, models []SpeechModelCost) SpeechAdvice {
	adv := SpeechAdvice{Backlog: &backlog}
	if rtf <= 0 {
		return adv
	}
	perDay := QuietHoursLength(quiet)
	days := func(r float64) float64 {
		return math.Round(backlog.Hours/r/perDay*10) / 10
	}
	adv.Estimate = &SpeechEstimate{Model: model, RTF: rtf, Measured: true, HoursPerDay: perDay, Days: days(rtf)}
	if adv.Estimate.Days <= SpeechSuggestDays {
		return adv
	}
	cost := -1.0
	for _, m := range models {
		if m.Name == model {
			cost = m.Cost
		}
	}
	var next *SpeechModelCost
	for i := range models {
		m := &models[i]
		if cost > 0 && m.Cost > 0 && m.Cost < cost && (next == nil || m.Cost > next.Cost) {
			next = m
		}
	}
	if next == nil {
		adv.Off = true
		return adv
	}
	r := math.Round(rtf*cost/next.Cost*1000) / 1000
	adv.Suggest = &SpeechEstimate{Model: next.Name, RTF: r, HoursPerDay: perDay, Days: days(r)}
	return adv
}

// backlog is the audio of the transcriptions waiting (nil: the database cannot say).
func (h *Handler) backlog(ctx context.Context) *SpeechBacklog {
	if h.DB == nil {
		return nil
	}
	var b SpeechBacklog
	var secs float64
	err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(v.duration_sec), 0)
  FROM processing_jobs j JOIN videos v ON v.id = j.video_id
 WHERE j.stage = 'transcribe' AND j.state IN ('pending', 'paused', 'claimed', 'running')`).Scan(&b.Files, &secs)
	if err != nil {
		return nil
	}
	b.Hours = math.Round(secs/3600*10) / 10
	return &b
}

func (h *Handler) speechGet(w http.ResponseWriter, r *http.Request) {
	if h.Supervisor == nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"available": false, "reason": errNoSupervisor.Error()})
		return
	}
	code, body, err := h.storageSupervisor().call(r.Context(), http.MethodGet, "speech", nil)
	if err == nil && code != http.StatusOK {
		err = &supervisorAnswer{code: code, body: body}
	}
	if err != nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"available": false, "reason": err.Error()})
		return
	}
	var out map[string]any
	var view speechView
	if json.Unmarshal(body, &out) != nil || json.Unmarshal(body, &view) != nil {
		httperror.Write(w, r, httperror.BadGateway("the supervisor's answer"))
		return
	}
	out["available"] = true
	if b := h.backlog(r.Context()); b != nil {
		rtf := 0.0
		if m := view.Measure; m != nil && m.Error == "" && m.Model == view.Speech.Repo {
			rtf = m.RTF
		}
		out["advice"] = Advise(*b, view.Speech.Model, rtf, view.QuietHours, view.Models)
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) speechPut(w http.ResponseWriter, r *http.Request) {
	if h.Supervisor == nil {
		httperror.Write(w, r, httperror.Conflict("no-supervisor", errNoSupervisor.Error()))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, common.MaxBodyBytes))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("body too large"))
		return
	}
	var in struct {
		Speech *string `json:"speech,omitempty"`
		Model  *string `json:"model,omitempty"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		httperror.Write(w, r, httperror.BadRequest("the speech settings are not a JSON object"))
		return
	}
	fwd, _ := json.Marshal(in)
	code, resp, err := h.storageSupervisor().call(r.Context(), http.MethodPut, "speech", fwd)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("the server's supervisor did not answer").Wrap(err))
		return
	}
	if code == http.StatusOK {
		h.audit(r, ActionSpeechChanged, map[string]any{"speech": in.Speech, "model": in.Model})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(resp)
}

func (h *Handler) speechMeasure(w http.ResponseWriter, r *http.Request) {
	if h.Supervisor == nil {
		httperror.Write(w, r, httperror.Conflict("no-supervisor", errNoSupervisor.Error()))
		return
	}
	code, resp, err := h.storageSupervisor().call(r.Context(), http.MethodPost, "speech/measure", []byte("{}"))
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("the server's supervisor did not answer").Wrap(err))
		return
	}
	if code == http.StatusAccepted {
		h.audit(r, ActionSpeechMeasured, map[string]any{})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(resp)
}
