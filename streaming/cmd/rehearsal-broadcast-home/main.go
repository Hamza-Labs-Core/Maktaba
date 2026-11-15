// Command rehearsal-broadcast-home is the home of the broadcast rehearsal (Epic 50 story 50.29):
// the streaming service's own broadcaster (internal/broadcast: Ctl, its ladder ffmpeg, the
// uploader, the state posts, the control socket and the slate) fed by a test-pattern channel (a
// burned clock and frame counter, software-encoded, VideoToolbox never used), with the published
// channel held in memory instead of the API's database. CI only: tools/rehearsal-cloud/broadcast.
//
// Its control port (default :8090) is the scenario's:
//
//	POST /publish        {"channel","token","ingest_url","rungs","name"} publishes the channel
//	POST /unpublish      takes it down (the broadcaster stops, posting on_air false)
//	POST /encoder/stop   stops the test pattern (the channel's own encoder dies; the ladder starves)
//	POST /encoder/start  starts it again
//	GET  /metrics        what the home sent: the uploader's own counts (the broadcaster's report)
//	                     and every PUT the home's HTTP client saw (the proxy counter), with times
//	GET  /healthz
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/relay"
)

func main() {
	addr := flag.String("addr", ":8090", "the control port")
	dir := flag.String("dir", "/tmp/broadcast-home", "the segments on their way out")
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "ffmpeg")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	h := newHome(*ffmpeg, *dir, logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go h.ctl.Run(ctx)
	go (&broadcast.Controls{Ctl: h.ctl, HTTP: h.client}).Run(ctx)

	srv := &http.Server{Addr: *addr, Handler: h.routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	logger.Info("rehearsal home listening", "addr", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("control port", "err", err)
		os.Exit(1)
	}
}

// home is the rehearsal's home: the broadcaster, its one channel and its counters.
type home struct {
	ctl     *broadcast.Ctl
	store   *memStore
	pattern *testPattern
	client  *http.Client
	puts    *putLog
}

// The test-pattern channel's own encode: 480p at 25 fps on a 2 s keyframe grid (the ladder
// copies it as src and encodes the lower rungs in software, which CI alone allows).
var output = broadcast.Output{Encoder: broadcast.EncSoftware, AllowSoftware: true,
	Width: 854, Height: 480, FPS: 25, KeyintSeconds: 2, VideoKbps: 1200, AudioKbps: 96}

func newHome(ffmpeg, dir string, logger *slog.Logger) *home {
	puts := &putLog{}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &countingTransport{next: http.DefaultTransport, log: puts}}
	store := &memStore{}
	pattern := &testPattern{ffmpeg: ffmpeg, logger: logger, on: true}
	ctl := &broadcast.Ctl{Store: store, Source: pattern, FFmpeg: ffmpeg, Dir: dir, HTTP: client, Logger: logger,
		Output: func(context.Context) broadcast.Output { return output }, Poll: time.Second}
	return &home{ctl: ctl, store: store, pattern: pattern, client: client, puts: puts}
}

type publishReq struct {
	Channel   string   `json:"channel"`
	Token     string   `json:"token"`
	IngestURL string   `json:"ingest_url"`
	Rungs     []string `json:"rungs"`
	Name      string   `json:"name"`
}

func (h *home) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]bool{"ok": true}) })
	mux.HandleFunc("POST /publish", func(w http.ResponseWriter, r *http.Request) {
		var req publishReq
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil ||
			req.Channel == "" || req.Token == "" || req.IngestURL == "" || len(req.Rungs) == 0 {
			http.Error(w, "body must be {channel, token, ingest_url, rungs, name}", http.StatusBadRequest)
			return
		}
		h.store.publish(broadcast.Job{Channel: uuid.NewSHA1(uuid.NameSpaceURL, []byte(req.Channel)), Name: req.Name,
			Rungs: req.Rungs, Mode: "always_on", State: "live",
			Creds: broadcast.Creds{Token: req.Token, IngestURL: req.IngestURL, Channel: req.Channel,
				ExpiresAt: time.Now().Add(23 * time.Hour)}})
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /unpublish", func(w http.ResponseWriter, _ *http.Request) {
		h.store.unpublish()
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /encoder/stop", func(w http.ResponseWriter, _ *http.Request) {
		h.pattern.set(false)
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /encoder/start", func(w http.ResponseWriter, _ *http.Request) {
		h.pattern.set(true)
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		state, detail, report := h.store.snapshot()
		writeJSON(w, map[string]any{"state": state, "detail": detail, "report": report,
			"running": len(h.ctl.Running()), "encoder_on": h.pattern.isOn(), "puts": h.puts.snapshot()})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// memStore is the published channel the API would hold (broadcast.Store), in memory.
type memStore struct {
	mu     sync.Mutex
	job    *broadcast.Job
	detail string
	report map[string]any
}

func (s *memStore) publish(j broadcast.Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job, s.detail, s.report = &j, "", nil
}

func (s *memStore) unpublish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil {
		s.job.State = "draft"
	}
}

func (s *memStore) Jobs(context.Context) ([]broadcast.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		return nil, nil
	}
	return []broadcast.Job{*s.job}, nil
}

func (s *memStore) SetState(_ context.Context, ch uuid.UUID, state, detail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil && s.job.Channel == ch {
		s.job.State, s.detail = state, detail
	}
	return nil
}

func (s *memStore) Report(_ context.Context, ch uuid.UUID, report map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil && s.job.Channel == ch {
		s.report = report
	}
	return nil
}

func (s *memStore) snapshot() (string, string, map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == nil {
		return "", "", nil
	}
	return s.job.State, s.detail, s.report
}

// testPattern is the channel's live TS (relay.Source): ffmpeg's testsrc2 with the wall clock and
// the frame number burned in, a tone, at real time. Off, it streams nothing, as a dead encoder.
type testPattern struct {
	ffmpeg string
	logger *slog.Logger

	mu   sync.Mutex
	on   bool
	kill context.CancelFunc
}

func (p *testPattern) set(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.on = on
	if !on && p.kill != nil {
		p.kill()
	}
}

func (p *testPattern) isOn() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.on
}

func (p *testPattern) Attach(uuid.UUID) func() { return func() {} }

// PatternArgs is the test pattern's ffmpeg: MPEG-TS on stdout.
func PatternArgs(o broadcast.Output, font string) []string {
	text := "drawtext=text='%{localtime\\:%T} #%{n}':x=24:y=24:fontsize=32:fontcolor=white:box=1:boxcolor=black@0.6"
	if font != "" {
		text = "drawtext=fontfile=" + font + ":" + text[len("drawtext="):]
	}
	return []string{"-hide_banner", "-loglevel", "warning", "-nostdin", "-re",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=%d", o.Width, o.Height, o.FPS),
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-vf", text,
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p",
		"-g", strconv.Itoa(o.FPS * o.KeyintSeconds), "-keyint_min", strconv.Itoa(o.FPS * o.KeyintSeconds), "-sc_threshold", "0",
		"-b:v", strconv.Itoa(o.VideoKbps) + "k", "-c:a", "aac", "-b:a", strconv.Itoa(o.AudioKbps) + "k",
		"-f", "mpegts", "pipe:1"}
}

const patternFont = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"

func (p *testPattern) Stream(ctx context.Context, _ uuid.UUID, w io.Writer, started func(relay.Origin)) error {
	p.mu.Lock()
	if !p.on {
		p.mu.Unlock()
		return errors.New("the test pattern is stopped")
	}
	sctx, cancel := context.WithCancel(ctx)
	p.kill = cancel
	p.mu.Unlock()
	defer cancel()

	font := ""
	if _, err := os.Stat(patternFont); err == nil {
		font = patternFont
	}
	cmd := exec.CommandContext(sctx, p.ffmpeg, PatternArgs(output, font)...) //nolint:gosec // the rehearsal's own arguments
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	started(relay.Origin{Host: "test-pattern", Gen: time.Now().UnixNano()})
	err := cmd.Wait()
	if sctx.Err() != nil {
		return sctx.Err()
	}
	return err
}

// putLog is the proxy counter: every segment PUT the home's HTTP client made, with its answer
// and when it finished, whatever the uploader itself counts.
type putLog struct {
	mu   sync.Mutex
	puts []putRecord
}

type putRecord struct {
	Rung   string `json:"rung"`
	Seq    int64  `json:"seq"`
	Status int    `json:"status"`
	Bytes  int64  `json:"bytes"`
	AtMS   int64  `json:"at_ms"`
}

func (l *putLog) add(r putRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.puts = append(l.puts, r)
}

func (l *putLog) snapshot() []putRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]putRecord(nil), l.puts...)
}

var segPath = regexp.MustCompile(`^/v1/ingest/[^/]+/([^/]+)/(\d+)\.ts$`)

type countingTransport struct {
	next http.RoundTripper
	log  *putLog
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m := segPath.FindStringSubmatch(req.URL.Path)
	resp, err := t.next.RoundTrip(req)
	if m == nil || req.Method != http.MethodPut {
		return resp, err
	}
	seq, _ := strconv.ParseInt(m[2], 10, 64)
	rec := putRecord{Rung: m[1], Seq: seq, Bytes: req.ContentLength, AtMS: time.Now().UnixMilli()}
	if err == nil {
		rec.Status = resp.StatusCode
	}
	t.log.add(rec)
	return resp, err
}
