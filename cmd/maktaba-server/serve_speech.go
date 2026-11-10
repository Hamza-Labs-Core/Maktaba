package main

// Speech to text on this machine (Epic 59 story 59.06): what the hardware tier and
// server.toml make of it, and how fast this machine transcribes, for Settings → This machine.
// The speech measure (the pipeline's stt/benchmark.py: two bundled 30-second clips) runs when
// speech is on and this machine has no measure of the model it runs (turned on, or a model
// change), and when an admin asks; its answer is <state>/stt/speed.json.
//
//	GET  /config/speech          the speech settings (supervisor.Speech), as saved and as
//	                             running, and the last measure ({measure: null}: none yet)
//	PUT  /config/speech          {"speech": "on"|"off"|"auto", "model": "base"|...|"auto"}:
//	                             server.toml's [transcription] speech and model, for the next
//	                             start (auto: the tier's)
//	POST /config/speech/measure  measure now (202; 409 while one runs or with speech off)

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// speechMeasureTimeout bounds a measure: two 30-second clips, the model's download first
// (large-v3-turbo is 1.6 GB on a slow line).
const speechMeasureTimeout = 90 * time.Minute

// speechRetryFailed is how long a failed measure of the same model waits before the next try.
const speechRetryFailed = 6 * time.Hour

// speechMeasure is the last measure (stt/benchmark.py's JSON), as Settings reads it.
type speechMeasure struct {
	Model      string  `json:"model"`
	Size       *string `json:"size"`
	Device     string  `json:"device,omitempty"`
	Threads    int     `json:"threads,omitempty"`
	Arch       string  `json:"arch,omitempty"`
	MeasuredAt string  `json:"measured_at,omitempty"`
	RTF        float64 `json:"rtf,omitempty"`
	Busy       bool    `json:"busy,omitempty"`
	Error      string  `json:"error,omitempty"`
	Clips      []struct {
		Lang string  `json:"lang"`
		RTF  float64 `json:"rtf"`
		WER  float64 `json:"wer"`
	} `json:"clips,omitempty"`
}

// speechRunner runs the measure, one at a time.
type speechRunner struct {
	opts supervisor.Options
	out  string
	log  io.Writer

	mu      sync.Mutex
	running bool
	// started is when the last measure started (the file's own time stands in for a failed
	// one's after a restart)
	started time.Time
	// failedRepo and failedAt: the last measure of that model ended with no answer of its own
	// (the pipeline could not start it): the watch waits speechRetryFailed before the next
	failedRepo string
	failedAt   time.Time
}

// speechHardware is the hardware speech is read from (tests replace it).
var speechHardware = supervisor.SpeechMachine

// speechMeasurer is this serve's runner (nil before serve builds it, and in a single role).
var speechMeasurer *speechRunner

func newSpeechRunner(opts supervisor.Options, log io.Writer) *speechRunner {
	return &speechRunner{opts: opts, out: speechMeasureFile(opts.Config), log: log}
}

// speechMeasureFile is where the measure is kept.
func speechMeasureFile(cfg config.Config) string {
	return filepath.Join(cfg.StateDir(), "stt", "speed.json")
}

// readSpeechMeasure is the measure in path (nil: none, or unreadable).
func readSpeechMeasure(path string) *speechMeasure {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m speechMeasure
	if json.Unmarshal(b, &m) != nil || m.Model == "" {
		return nil
	}
	return &m
}

// stale reports whether speech s needs a measure, given the last one m (nil: none) made at
// at: none yet, another model's, or a failed one older than speechRetryFailed.
func speechStale(s supervisor.Speech, m *speechMeasure, at time.Time, now time.Time) bool {
	if !s.On || s.Engine != "ct2" || s.Repo == "" {
		return false
	}
	switch {
	case m == nil, m.Model != s.Repo:
		return true
	case m.Error != "":
		return now.Sub(at) > speechRetryFailed
	}
	return false
}

// measuring reports whether a measure runs now.
func (r *speechRunner) measuring() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// start runs a measure in the background (false: one runs already).
func (r *speechRunner) start(ctx context.Context, why, repo string) bool {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return false
	}
	r.running, r.started = true, time.Now()
	r.mu.Unlock()
	go func() {
		defer func() {
			r.mu.Lock()
			r.running = false
			r.mu.Unlock()
		}()
		r.run(ctx, why, repo)
	}()
	return true
}

func (r *speechRunner) run(ctx context.Context, why, repo string) {
	if err := os.MkdirAll(filepath.Dir(r.out), 0o750); err != nil {
		fmt.Fprintf(r.log, "[serve] level=warn the speech measure: %v\n", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, speechMeasureTimeout)
	defer cancel()
	c, err := supervisor.SpeechMeasureCmd(ctx, r.opts, r.out)
	if err != nil {
		fmt.Fprintf(r.log, "[serve] level=warn the speech measure: %v\n", err)
		return
	}
	c.Stderr = r.log
	fmt.Fprintf(r.log, "[serve] the speech measure starts (%s)\n", why)
	t0 := time.Now()
	out, err := c.Output()
	m := readSpeechMeasure(r.out)
	if err != nil && (m == nil || m.Model != repo) {
		r.mu.Lock()
		r.failedRepo, r.failedAt = repo, time.Now()
		r.mu.Unlock()
	}
	switch {
	case m != nil && m.Error != "":
		fmt.Fprintf(r.log, "[serve] level=warn the speech measure of %s: %s\n", m.Model, m.Error)
	case err != nil:
		fmt.Fprintf(r.log, "[serve] level=warn the speech measure failed after %s: %v %s\n",
			time.Since(t0).Round(time.Second), err, strings.TrimSpace(string(out)))
	case m != nil:
		fmt.Fprintf(r.log, "[serve] the speech measure: %s transcribes at %.2f× real time on %s (%d threads%s)\n",
			m.Model, m.RTF, m.Device, m.Threads, map[bool]string{true: ", the machine busy", false: ""}[m.Busy])
	}
}

// backingOff reports whether the last measure of repo failed without an answer less than
// speechRetryFailed ago.
func (r *speechRunner) backingOff(repo string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failedRepo == repo && now.Sub(r.failedAt) < speechRetryFailed
}

// watch measures when speech needs it: at the start (once the pipeline had a minute to
// build its environment) and then every 10 minutes, until ctx ends. A machine whose
// pipeline has no speech engine writes a failed measure, tried again after
// speechRetryFailed.
func (r *speechRunner) watch(ctx context.Context) {
	first := time.NewTimer(2 * time.Minute)
	defer first.Stop()
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-tick.C:
		}
		s := supervisor.SpeechOf(r.opts.Config, speechHardware())
		m := readSpeechMeasure(r.out)
		at := r.started
		if st, err := os.Stat(r.out); err == nil && st.ModTime().After(at) {
			at = st.ModTime()
		}
		if !r.measuring() && speechStale(s, m, at, time.Now()) && !r.backingOff(s.Repo, time.Now()) {
			why := "speech is on and " + s.Model + " was not measured here"
			if m != nil && m.Model != s.Repo {
				why = "the model changed to " + s.Model
			}
			r.start(ctx, why, s.Repo)
		}
	}
}

// speechDoc is GET /config/speech's answer.
type speechDoc struct {
	// Speech is as server.toml saves it (what the next start runs); Running as this start
	// runs it; RestartNeeded whether they differ.
	Speech        supervisor.Speech  `json:"speech"`
	Running       *supervisor.Speech `json:"running,omitempty"`
	RestartNeeded bool               `json:"restart_needed"`
	CanRestart    bool               `json:"can_restart"`
	Measure       *speechMeasure     `json:"measure"`
	Measuring     bool               `json:"measuring"`
	// QuietHours is the window heavy work waits for ("": none), for the backlog's estimate.
	QuietHours string `json:"quiet_hours"`
	// Models are the offered sizes: download, a worker's memory, the relative CPU cost the
	// estimate of a model not measured scales by.
	Models []hostinfo.SpeechModel `json:"models"`
}

// runningSpeech is what this start runs with (nil before serve resolves it).
var runningSpeech *supervisor.Speech

func (c *configAdmin) speechDocOf(file config.Config) speechDoc {
	d := speechDoc{Speech: supervisor.SpeechOf(file, speechHardware()), Running: runningSpeech,
		CanRestart: c.restart != nil || c.reload != nil}
	if run := runningSpeech; run != nil {
		d.RestartNeeded = run.On != d.Speech.On || run.Repo != d.Speech.Repo
	}
	d.Measure = readSpeechMeasure(speechMeasureFile(file))
	if speechMeasurer != nil {
		d.Measuring = speechMeasurer.measuring()
	}
	if q := runningQuiet.Load(); q != nil {
		d.QuietHours = *q
	} else {
		d.QuietHours = config.QuietHoursOf(file, hostinfo.DefaultsFor(tierOf(file.Server, speechHardware()).Tier, speechHardware()).QuietHours)
	}
	d.Models = []hostinfo.SpeechModel{}
	for _, name := range d.Speech.Offered {
		if m, ok := hostinfo.SpeechModelNamed(name); ok {
			d.Models = append(d.Models, m)
		}
	}
	return d
}

// speechRoutes answers /config/speech (false: not its path).
func (c *configAdmin) speechRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/config/speech", "/config/speech/measure":
	default:
		return false
	}
	file, _, err := config.LoadFrom(c.path)
	if err != nil {
		writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return true
	}
	switch {
	case r.URL.Path == "/config/speech" && r.Method == http.MethodGet:
		writeConfigJSON(w, http.StatusOK, c.speechDocOf(file))
	case r.URL.Path == "/config/speech" && r.Method == http.MethodPut:
		c.speechPut(w, r)
	case r.URL.Path == "/config/speech/measure" && r.Method == http.MethodPost:
		s := supervisor.SpeechOf(file, speechHardware())
		switch {
		case speechMeasurer == nil:
			writeConfigJSON(w, http.StatusConflict, map[string]string{"error": "no_runner", "detail": "this machine runs no pipeline"})
		case !s.On || s.Engine != "ct2":
			writeConfigJSON(w, http.StatusConflict, map[string]string{"error": "speech_off", "detail": "speech to text is off or not measured on this machine"})
		case !speechMeasurer.start(context.Background(), "an admin asked", s.Repo):
			writeConfigJSON(w, http.StatusConflict, map[string]string{"error": "measuring", "detail": "a measure runs already"})
		default:
			writeConfigJSON(w, http.StatusAccepted, c.speechDocOf(file))
		}
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
	return true
}

func (c *configAdmin) speechPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Speech *string `json:"speech"`
		Model  *string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
		writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": "the body: " + err.Error()})
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	file, _, err := config.LoadFrom(c.path)
	if err != nil {
		writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	errs := map[string]string{}
	speech, model := file.Transcription.Speech, file.Transcription.Model
	if body.Speech != nil {
		switch v := strings.ToLower(strings.TrimSpace(*body.Speech)); v {
		case "on", "off":
			speech = v
		case "auto":
			speech = ""
		default:
			errs["speech"] = "on, off or auto"
		}
	}
	if body.Model != nil {
		v := strings.TrimSpace(*body.Model)
		offered := supervisor.SpeechOf(file, speechHardware()).Offered
		switch {
		case v == "auto":
			model = ""
		case slices.Contains(offered, v):
			model = v
		default:
			errs["model"] = "auto or one of " + strings.Join(offered, ", ")
		}
	}
	if len(errs) > 0 {
		writeConfigJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}
	file.Transcription.Speech, file.Transcription.Model = speech, model
	if err := file.SaveTo(c.path); err != nil {
		writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	fmt.Fprintf(os.Stderr, "[serve] speech to text changed from the web (speech %q, model %q); it applies at the next start\n", speech, model)
	writeConfigJSON(w, http.StatusOK, c.speechDocOf(file))
}
