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
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
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
	ErrNotFound           = errors.New("not-found")
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
	Probe      *probe.Cache
	Profiles   *capability.Registry
	Sessions   session.Store
	Allocator  *slots.Allocator
	HWAccel    ffmpeg.HWAccel
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
			return OpenSessionResponse{}, ErrFailedPrecondition
		}
		return OpenSessionResponse{}, ErrNotFound
	}

	// Dedupe: existing open session for the same (user, video).
	if existing, ok, _ := s.Sessions.ActiveByUserVideo(ctx, userUUID, videoUUID); ok && existing != nil {
		return s.respFor(existing, ladderFor(row, req)), nil
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
	state := session.StateActive

	if mode == session.ModeTranscode {
		decision, _ := s.Allocator.Decide(slots.Request{
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
			return OpenSessionResponse{}, ErrResourceExhausted
		}
	}

	format := session.FormatHLS
	if strings.EqualFold(req.Format, "dash") {
		format = session.FormatDASH
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
		handle, terr := s.Transcode.Start(ctx, ffmpeg.Job{
			SessionID:  sessID.String(),
			InputPath:  row.Path,
			OutputDir:  s.ResolveDir(sessID.String()),
			Ladder:     ladder,
			HWAccel:    string(s.HWAccel),
			Format:     fmtStr,
			MasterName: "master.m3u8",
		})
		if terr != nil {
			return OpenSessionResponse{}, ErrFailedPrecondition
		}
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
		return OpenSessionResponse{}, err
	}

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
	}
	if row.State == session.StateQueued {
		resp.Queue = &QueueState{Position: s.Allocator.QueueLength(), ETASec: 30}
	}
	return resp
}

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
	return s.Sessions.Close(ctx, id, session.ReasonAPI, s.Now().UTC())
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
		HWAccel:             string(s.HWAccel),
		MaxBitrateKbps:      40000,
		SupportedContainers: []string{"mp4", "mkv", "webm", "mov", "ts"},
		TranscodeUsed:       s.Allocator.Used(),
		TranscodeCapacity:   s.Allocator.MaxConcurrent(),
	}
	if s.HWAccel == "" {
		c.HWAccel = "software"
	}
	return c, nil
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
