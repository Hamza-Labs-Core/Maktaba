// Package grpcsrv exposes the Streaming service's gRPC surface
// (Story 8.8) as a Go-native interface. The wire schema for this
// interface is in shared/proto/streaming.proto (planned outside this
// module); here we expose what the API client (api/internal/grpcclients/streaming)
// already names so the two halves stay drift-free.
//
// Production wiring instantiates google.golang.org/grpc and registers
// a generated server; for the v1 in-process deployment the API can
// use Server directly. Tests exercise this struct so the contract
// stays solid through the migration.
package grpcsrv

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slots"
)

// OpenSessionRequest mirrors §9.9's proto shape.
type OpenSessionRequest struct {
	VideoID        string
	UserID         string
	ClientProfile  string
	AudioTrack     int
	SubtitleTrack  int
	StartSec       int
	MaxBitrateKbps int
	Format         string // "hls" | "dash" — defaults to hls
	ForceSoftware  bool
	ForceTranscode bool
	BurnSubs       bool
	AcceptQueue    bool
}

// Rendition mirrors the proto Rendition message.
type Rendition struct {
	Name        string
	Width       int
	Height      int
	BitrateKbps int
}

// QueueState is populated when the response state == "queued".
type QueueState struct {
	Position int
	ETASec   int
}

// OpenSessionResponse mirrors the proto reply.
type OpenSessionResponse struct {
	SessionID    string
	Mode         string
	Ladder       []Rendition
	ManifestPath string
	ExpiresAt    time.Time
	State        string // "active" | "queued"
	Queue        *QueueState
	// StartSec is where in the video the stream starts (seconds): an HLS
	// transcode opened at a position starts there (its playlist lists the part
	// before as gaps, its times the video's); 0 for the others.
	StartSec int
}

// Capabilities mirrors GetCapabilities response.
type Capabilities struct {
	Codecs              []string
	HWAccel             string
	MaxBitrateKbps      int
	SupportedContainers []string
	TranscodeUsed       int
	TranscodeCapacity   int
}

// Status mirrors the HealthCheck reply.
type Status struct {
	Healthy bool
	Detail  string
}

// Errors. Map to gRPC codes at the wire boundary.
var (
	ErrFailedPrecondition = errors.New("failed-precondition")
	ErrResourceExhausted  = errors.New("resource-exhausted")
	// ErrNoDiskSpace: too little free on the cache's disk to start a
	// transcode (TranscodeRoom); the API says so, not "busy".
	ErrNoDiskSpace = errors.New("insufficient-storage")
	ErrNotFound    = errors.New("not-found")
)

// TranscodeOrchestrator is the seam grpcsrv.OpenSession uses to spawn
// FFmpeg for transcode-mode sessions (HLB-328). Production wires
// ffmpeg.DefaultOrchestrator (real ffmpeg via Spawn); unit tests inject
// a fake that writes canned output and never execs ffmpeg.
type TranscodeOrchestrator interface {
	Start(ctx context.Context, job ffmpeg.Job) (*ffmpeg.Handle, error)
}

// Server is the in-process surface. The API holds one of these and
// calls methods directly; production replaces it with a thin grpc
// handler that delegates to the same struct.
type Server struct {
	Probe     *probe.Cache
	Profiles  *capability.Registry
	Sessions  session.Store
	Allocator *slots.Allocator
	HWAccel   ffmpeg.HWAccel
	// HWAccelNow, when set, is the verdict now (ffmpeg.WatchChoice keeps it
	// current) and wins over HWAccel.
	HWAccelNow func() ffmpeg.HWAccel
	Host       string // hostname, recorded in row.Host
	Now        func() time.Time
	SessionTTL time.Duration // expires_at = now + TTL (default 30 min)

	// Transcode spawns FFmpeg for transcode-mode sessions (HLB-328).
	// Nil → no FFmpeg is spawned (the pre-HLB-328 behaviour: manifest
	// requests 404 until something writes the playlist). main.go wires
	// ffmpeg.DefaultOrchestrator.
	Transcode TranscodeOrchestrator

	// ResolveDir maps a session id to its on-disk HLS output dir
	// (cache/hls/{session_id}). Required when Transcode is set so the
	// orchestrator and the manifest handler agree on the directory.
	ResolveDir func(sessID string) string

	// TranscodeRoom, when set, says whether a new transcode may start (a nil
	// error): its folder keeps every segment until the session closes, so
	// main.go refuses one with too little free on the cache's disk.
	TranscodeRoom func() error

	// Logger receives the session lifecycle (opened, reused, closed, refused);
	// nil: slog's default.
	Logger *slog.Logger
}

func (s *Server) log() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

// stderrOf is the last of a session's encoder's stderr ("" without one).
func stderrOf(t session.Transcoder) string {
	if h, ok := t.(interface{ Stderr() string }); ok && h != nil {
		return h.Stderr()
	}
	return ""
}

// New returns a Server with sensible defaults filled in.
func New(probe *probe.Cache, sessions session.Store, allocator *slots.Allocator, profiles *capability.Registry) *Server {
	return &Server{
		Probe:      probe,
		Profiles:   profiles,
		Sessions:   sessions,
		Allocator:  allocator,
		Now:        time.Now,
		SessionTTL: 30 * time.Minute,
		Host:       hostname(),
	}
}

func hostname() string {
	if h, _ := osHostname(); h != "" {
		return h
	}
	return "unknown"
}

// OpenSession (Story 8.8 AC-1) — looks up the probe, decides mode,
// admits a slot, persists the session row, returns the manifest path.
func (s *Server) OpenSession(ctx context.Context, req OpenSessionRequest) (OpenSessionResponse, error) {
	videoUUID, err := uuid.Parse(req.VideoID)
	if err != nil {
		return OpenSessionResponse{}, errors.New("video_id not a UUID")
	}
	userUUID, err := uuid.Parse(req.UserID)
	if err != nil {
		return OpenSessionResponse{}, errors.New("user_id not a UUID")
	}

	row, err := s.Probe.Lookup(ctx, videoUUID)
	if err != nil {
		if errors.Is(err, probe.ErrNotProbed) {
			s.log().Warn("vod session refused: the video is not probed yet",
				"video_id", req.VideoID, "event", "vod_open_not_probed")
			return OpenSessionResponse{}, ErrFailedPrecondition
		}
		s.log().Warn("vod session refused: the video's probe could not be read",
			"video_id", req.VideoID, "err", err.Error(), "event", "vod_open_probe_failed")
		return OpenSessionResponse{}, ErrNotFound
	}

	// Dedupe: the user's open sessions of the video. One whose transcode ended
	// short of the end (it failed) is closed: the player opening the video
	// again (its playlist answered 410) gets a new transcode, not the same dead
	// one. One whose stream reaches the position asked for is handed back (the
	// latest to start before it). None: a new one starts there, beside the
	// others — the user's other device plays on in its own, which the reaper
	// closes once nothing fetches from it.
	open, err := s.Sessions.OpenByUserVideo(ctx, userUUID, videoUUID)
	if err != nil {
		s.log().Warn("vod sessions of the video not listed: opening a new one",
			"video_id", req.VideoID, "err", err.Error(), "event", "vod_open_dedupe_failed")
	}
	var reuse *session.Row
	for _, existing := range open {
		switch {
		case existing.TranscodeCut():
			s.log().Warn("vod session closed: its transcode ended short of the video's end",
				"session_id", existing.ID.String(), "video_id", req.VideoID,
				"reason", string(session.ReasonTranscodeFailed),
				"stderr", lastStderrLines(stderrOf(existing.Transcoder)), "event", "vod_session_closed")
			if cerr := s.Sessions.Close(ctx, existing.ID, session.ReasonTranscodeFailed, s.Now().UTC()); cerr != nil {
				s.log().Warn("vod session not closed", "session_id", existing.ID.String(),
					"err", cerr.Error(), "event", "vod_session_close_failed")
			}
		case s.reaches(existing, req.StartSec) && (reuse == nil || existing.StartSec >= reuse.StartSec):
			reuse = existing
		}
	}
	if reuse != nil {
		s.log().Info("vod session reused", "session_id", reuse.ID.String(), "video_id", req.VideoID,
			"mode", string(reuse.Mode), "start_sec", req.StartSec, "event", "vod_session_reused")
		return s.respFor(reuse, ladderFor(row, req)), nil
	}

	profile, _ := s.Profiles.Get(req.ClientProfile)
	src := capability.Source{
		Container: row.Container, VideoCodec: row.VideoCodec, AudioCodec: row.AudioCodec,
		Height: row.Height, BitrateKbps: row.BitrateKbps, AudioChannels: row.AudioChannels,
		HDR: row.HDR,
	}
	verdict := s.Profiles.Decide(profile, src, capability.Override{
		ForceTranscode: req.ForceTranscode,
		MaxBitrateKbps: req.MaxBitrateKbps,
	})

	mode := session.Mode(verdict.Mode)
	if mode == session.ModeRemux {
		// Nothing serves a remux yet (Story 8.4's cache-then-stream Remuxer has no
		// runtime caller), so a remux session's playlist would never be written: the
		// player waits on a 404 for ever. An HLS transcode plays it.
		mode = session.ModeTranscode
	}
	state := session.StateActive

	// the transcode slot taken (""; none), given back when the transcode
	// ends, the session closes, or the open fails
	holdID := ""
	release := func() {
		if holdID != "" {
			_ = s.Allocator.Release(holdID)
		}
	}
	if mode == session.ModeTranscode {
		// before a slot is taken: a refused open holds none
		if s.TranscodeRoom != nil && s.TranscodeRoom() != nil {
			return OpenSessionResponse{}, ErrNoDiskSpace
		}
		var decision slots.Decision
		decision, holdID = s.Allocator.Decide(slots.Request{
			CanDirectCap: row.Height >= 720 && verdict.Mode == capability.ModeTranscode,
			AcceptQueue:  req.AcceptQueue,
		})
		switch decision {
		case slots.DecisionAdmit:
			// proceed
		case slots.DecisionDirectCap:
			mode = session.ModeDirectDegraded
		case slots.DecisionQueue:
			state = session.StateQueued
		case slots.DecisionExhausted:
			s.log().Warn("vod session refused: every transcode slot is taken",
				"video_id", req.VideoID, "event", "vod_slots_exhausted")
			return OpenSessionResponse{}, ErrResourceExhausted
		}
	}

	format := session.FormatHLS
	if strings.EqualFold(req.Format, "dash") {
		format = session.FormatDASH
	}
	// an HLS transcode starts where the viewer is (a resume, a reopen)
	start := 0
	if mode == session.ModeTranscode && format == session.FormatHLS && req.StartSec > 0 {
		start = req.StartSec
	}

	sessID := uuid.New()
	ladder := ladderFor(row, req)

	// HLB-328: spawn FFmpeg for active transcode sessions *before*
	// persisting the row, so the stored row carries the live handle +
	// pid and the reaper / CloseSession can kill the child on
	// idle/close. Before this, HLSArgs/DASHArgs had no runtime caller,
	// so the manifest handler served files nothing ever wrote → every
	// manifest/segment 404'd in production. We spawn via the Transcode
	// seam (real ffmpeg in prod, fake in tests). Spawn failures abort
	// the open with ErrFailedPrecondition rather than handing the
	// client a session whose manifest will never materialise.
	var transcoder session.Transcoder
	pid := 0
	if mode == session.ModeTranscode && state == session.StateActive && s.Transcode != nil {
		if s.ResolveDir == nil {
			return OpenSessionResponse{}, errors.New("transcode configured without ResolveDir")
		}
		fmtStr := "hls"
		if format == session.FormatDASH {
			fmtStr = "dash"
		}
		hw := s.hwaccel()
		if req.ForceSoftware {
			hw = ffmpeg.HWAccelSoftware
		}
		var colour *ffmpeg.Colour
		if c, ok := ffmpeg.CatalogueColour(row.ColorTransfer, row.ColorMatrix, row.ColorPrimaries, row.ColorRange, row.PixFmt); ok {
			colour = &c // no probe of the file at the open (a NAS can take seconds)
		}
		handle, terr := s.Transcode.Start(ctx, ffmpeg.Job{
			SessionID:   sessID.String(),
			InputPath:   pathmap.Resolve(row.Path), // story 31.3: canonical -> local
			OutputDir:   s.ResolveDir(sessID.String()),
			Ladder:      ladder,
			HWAccel:     string(hw),
			Format:      fmtStr,
			MasterName:  "master.m3u8",
			SourceCodec: row.VideoCodec,
			NoAudio:     row.AudioCodec == "" && row.AudioChannels == 0,
			StartSec:    start,
			Colour:      colour,
		})
		if terr != nil {
			release()
			s.log().Error("vod transcode did not start", "session_id", sessID.String(),
				"video_id", req.VideoID, "input", pathmap.Resolve(row.Path), "hwaccel", string(hw),
				"err", terr.Error(), "event", "vod_transcode_start_failed")
			// the reason is in the log line above: it names this host's paths,
			// which the API would hand the player
			return OpenSessionResponse{}, ErrFailedPrecondition
		}
		handle.OnEnd(release) // the slot is the encoder's, not the viewer's
		transcoder = handle
		pid = handle.PID()
	}

	sessRow := &session.Row{
		ID:            sessID,
		VideoID:       videoUUID,
		UserID:        userUUID,
		ClientProfile: profile.Name,
		Mode:          mode,
		Format:        format,
		Host:          s.Host,
		PID:           pid,
		StartedAt:     s.Now().UTC(),
		LastSegmentAt: s.Now().UTC(),
		State:         state,
		Transcoder:    transcoder,
		SlotHold:      holdID,
		StartSec:      start,
	}
	if err := s.Sessions.Insert(ctx, sessRow); err != nil {
		// Don't leak the FFmpeg child if the row can't be stored.
		// HLB-328 I2: use a fresh background-scoped ctx, NOT the request
		// ctx — the request ctx is about to be cancelled by grpc-go, and
		// Process.Stop's select{case <-ctx.Done()} would return
		// context.Canceled before SIGTERM/SIGKILL is confirmed, orphaning
		// ffmpeg. The timeout covers the SIGTERM grace window plus a
		// margin so termination is always confirmed.
		if transcoder != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = transcoder.Stop(stopCtx)
			cancel()
		}
		release()
		s.log().Error("vod session not stored", "session_id", sessID.String(), "video_id", req.VideoID,
			"err", err.Error(), "event", "vod_session_insert_failed")
		return OpenSessionResponse{}, err
	}

	s.log().Info("vod session opened", "session_id", sessID.String(), "video_id", req.VideoID,
		"mode", string(mode), "state", string(state), "verdict", verdict.Reason,
		"source", row.Container+"/"+row.VideoCodec+"/"+row.AudioCodec, "height", row.Height,
		"start_sec", start, "pid", pid, "event", "vod_session_opened")
	return s.respFor(sessRow, ladder), nil
}

func (s *Server) respFor(row *session.Row, ladder []ffmpeg.Rendition) OpenSessionResponse {
	rends := make([]Rendition, 0, len(ladder))
	for _, l := range ladder {
		rends = append(rends, Rendition{Name: l.Name, Width: l.Width, Height: l.Height, BitrateKbps: l.BitrateKbps})
	}
	manifestPath := "/stream/" + row.ID.String() + "/manifest." + manifestExt(row.Format)
	if row.Mode == session.ModeDirect || row.Mode == session.ModeDirectDegraded {
		manifestPath = "/stream/direct/" + row.VideoID.String()
	}
	resp := OpenSessionResponse{
		SessionID:    row.ID.String(),
		Mode:         string(row.Mode),
		Ladder:       rends,
		ManifestPath: manifestPath,
		ExpiresAt:    s.Now().UTC().Add(s.SessionTTL),
		State:        string(row.State),
		StartSec:     row.StartSec,
	}
	if row.State == session.StateQueued {
		resp.Queue = &QueueState{Position: s.Allocator.QueueLength(), ETASec: 30}
	}
	return resp
}

// reachSlack is how far past what an open HLS transcode has written a position
// may be for the session to serve it: its transcode gets there soon.
const reachSlack = time.Minute

// reaches reports whether the open session's stream serves a viewer at start
// (seconds): an HLS transcode from its own start to what it has written (its
// first variant playlist), and a little more; the rest, anywhere.
func (s *Server) reaches(row *session.Row, start int) bool {
	if row.Mode != session.ModeTranscode || row.Format != session.FormatHLS || s.ResolveDir == nil {
		return true
	}
	if start < row.StartSec {
		return false
	}
	if row.TranscodeFinished() {
		return true
	}
	data, err := os.ReadFile(filepath.Join(s.ResolveDir(row.ID.String()), "v0", "index.m3u8"))
	if err != nil {
		data = nil // nothing written yet
	}
	written := 0.0
	for _, m := range extinf.FindAllSubmatch(data, -1) {
		if d, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
			written += d
		}
	}
	return float64(start) <= float64(row.StartSec)+written+reachSlack.Seconds()
}

// extinf is a media playlist's segment duration.
var extinf = regexp.MustCompile(`#EXTINF:([0-9.]+)`)

func manifestExt(f session.Format) string {
	if f == session.FormatDASH {
		return "mpd"
	}
	return "m3u8"
}

func ladderFor(row *probe.Row, req OpenSessionRequest) []ffmpeg.Rendition {
	kbps := req.MaxBitrateKbps
	if kbps == 0 {
		kbps = row.BitrateKbps
	}
	return ffmpeg.DefaultLadder(kbps)
}

// CloseSession (AC-2) marks a session closed. Idempotent.
func (s *Server) CloseSession(ctx context.Context, sessionID string) error {
	id, err := uuid.Parse(sessionID)
	if err != nil {
		return errors.New("session_id not a UUID")
	}
	row, ok, err := s.Sessions.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if row.ClosedAt != nil {
		return nil
	}
	err = s.Sessions.Close(ctx, id, session.ReasonAPI, s.Now().UTC())
	if err != nil {
		s.log().Warn("vod session not closed", "session_id", sessionID, "err", err.Error(),
			"event", "vod_session_close_failed")
	} else {
		s.log().Info("vod session closed", "session_id", sessionID, "reason", string(session.ReasonAPI),
			"event", "vod_session_closed")
	}
	if row.SlotHold != "" {
		// its transcode's end gives it back too (closing stops it); a session
		// without one (no orchestrator) only here
		_ = s.Allocator.Release(row.SlotHold)
	}
	return err
}

// EvictHashCache (AC-3) drops probe-cache entries for a content hash.
// Pipeline calls this after re-probing a file that may have been
// modified in place.
func (s *Server) EvictHashCache(_ context.Context, hash string) (int, error) {
	if hash == "" {
		return 0, errors.New("hash empty")
	}
	return s.Probe.EvictHash(hash), nil
}

// GetCapabilities (AC-4) reports the host's encoding support and slot usage.
func (s *Server) GetCapabilities(_ context.Context) (Capabilities, error) {
	c := Capabilities{
		Codecs:              []string{"h264", "aac"},
		HWAccel:             string(s.hwaccel()),
		MaxBitrateKbps:      40000,
		SupportedContainers: []string{"mp4", "mkv", "webm", "mov", "ts"},
		TranscodeUsed:       s.Allocator.Used(),
		TranscodeCapacity:   s.Allocator.MaxConcurrent(),
	}
	if c.HWAccel == "" {
		c.HWAccel = "software"
	}
	return c, nil
}

// hwaccel is the transcodes' encoder verdict now.
func (s *Server) hwaccel() ffmpeg.HWAccel {
	if s.HWAccelNow != nil {
		return s.HWAccelNow()
	}
	return s.HWAccel
}

// HealthCheck reports overall service status.
func (s *Server) HealthCheck(_ context.Context) (Status, error) {
	return Status{Healthy: true, Detail: "ok cores=" + itoa(runtime.NumCPU())}, nil
}

// Tiny helpers — keep this file self-contained without pulling stdlib
// imports beyond what the surface needs.
func itoa(n int) string { return formatInt(int64(n)) }

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := []byte{}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte("-"), buf...)
	}
	return string(buf)
}

// osHostname is broken out so tests can stub it.
var osHostname = func() (string, error) {
	if h := getHostnameEnv(); h != "" {
		return h, nil
	}
	return "", nil
}

func getHostnameEnv() string {
	for _, k := range []string{"HOSTNAME", "HOST"} {
		if v := getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// getenv is overridable for tests.
var getenv = func(_ string) string { return "" }

// lastStderrLines is the last few lines of an encoder's stderr, for a log line.
func lastStderrLines(stderr string) string {
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	return strings.Join(lines, "\n")
}
