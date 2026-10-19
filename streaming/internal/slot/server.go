package slot

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

// BeatInterval is how often a slot writes its tv_slots row (AC4: three missed
// beats, 15 s, make it unhealthy).
const BeatInterval = 5 * time.Second

// holdInterval: how often held channels are started/refreshed.
const holdInterval = 2 * time.Second

// Engine is what a slot server needs of the channel engine.
type Engine interface {
	Hold(ctx context.Context, id uuid.UUID) error
	StopChannel(id uuid.UUID) bool
	ActiveIDs() []uuid.UUID
	StopAll()
}

// Server is a slot's HTTP surface and loops.
type Server struct {
	State   *State
	HostID  string
	BaseURL string
	Version string
	Engine  Engine
	// Channels mounts /stream/channel/{id}/… (channel.HTTPHandler.Mount).
	Channels func(chi.Router)
	Pool     *pgxpool.Pool
	Logger   *slog.Logger
	// EncodersUsed reports the hardware sessions in use (beat, status).
	EncodersUsed func() int
	// Now (tests).
	Now func() time.Time

	started time.Time
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Handler is the slot's public handler: loopback only (AC2), the channel routes
// and the internal slot routes.
func (s *Server) Handler() http.Handler {
	if s.started.IsZero() {
		s.started = s.now()
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !Loopback(req.RemoteAddr) {
				http.Error(w, "a TV slot answers this machine only", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/internal/tv/slot/status", s.status)
	r.Post("/internal/tv/slot/warm", s.warm)
	r.Post("/internal/tv/slot/drain", s.drain)
	if s.Channels != nil {
		s.Channels(r)
	}
	return r
}

// Status is GET /internal/tv/slot/status.
type Status struct {
	Slot         string   `json:"slot"`
	Role         string   `json:"role"`
	Epoch        int64    `json:"epoch"`
	HostID       string   `json:"host_id"`
	Release      string   `json:"release,omitempty"`
	Mismatch     bool     `json:"release_mismatch,omitempty"`
	Version      string   `json:"version"`
	PID          int      `json:"pid"`
	StartedAt    string   `json:"started_at"`
	Stations     []string `json:"stations"`
	Warm         []string `json:"warm"`
	EncodersUsed int      `json:"encoders_used"`
	Budget       Budget   `json:"budget"`
	// The fixed stream layout (story 33.4 AC3).
	PMTPID    int `json:"pmt_pid"`
	VideoPID  int `json:"video_pid"`
	AudioPID  int `json:"audio_pid"`
	ServiceID int `json:"service_id"`
}

// Snapshot is the slot's status.
func (s *Server) Snapshot() Status {
	role, epoch := s.State.Role()
	st := Status{Slot: s.State.Name, Role: role, Epoch: epoch, HostID: s.HostID, Release: s.State.Release,
		Mismatch: s.State.Mismatch(), Version: s.Version, PID: os.Getpid(),
		StartedAt: s.started.UTC().Format(time.RFC3339), Budget: s.State.Budget(),
		PMTPID: channel.TSPMTPID, VideoPID: channel.TSVideoPID, AudioPID: channel.TSAudioPID,
		ServiceID: channel.TSServiceID, Stations: []string{}, Warm: []string{}}
	for _, id := range s.Engine.ActiveIDs() {
		st.Stations = append(st.Stations, id.String())
	}
	for _, id := range s.State.Holds(s.now()) {
		st.Warm = append(st.Warm, id.String())
	}
	sort.Strings(st.Stations)
	sort.Strings(st.Warm)
	if s.EncodersUsed != nil {
		st.EncodersUsed = s.EncodersUsed()
	}
	return st
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Snapshot())
}

// WarmRequest is POST /internal/tv/slot/warm: the front door's view of this
// slot (role, epoch), its encoder budget and the channels to keep warm.
// Exclusive: channels on air here but not in the set stop now (the hot standby
// keeps exactly its warm set, within its budget).
type WarmRequest struct {
	Role      string   `json:"role"`
	Epoch     int64    `json:"epoch"`
	Channels  []string `json:"channels"`
	Budget    Budget   `json:"budget"`
	Exclusive bool     `json:"exclusive"`
}

func (s *Server) warm(w http.ResponseWriter, r *http.Request) {
	var req WarmRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if req.Role != "" {
		if s.State.SetRole(req.Role, req.Epoch) {
			s.log().Info("tv slot: role", "role", req.Role, "epoch", req.Epoch,
				"event", "tv_slot_role")
		}
	}
	s.State.SetBudget(req.Budget)
	ids := make([]uuid.UUID, 0, len(req.Channels))
	want := map[uuid.UUID]bool{}
	for _, c := range req.Channels {
		if id, err := uuid.Parse(c); err == nil {
			ids = append(ids, id)
			want[id] = true
		}
	}
	s.State.SetHolds(ids, s.now())
	if req.Exclusive {
		for _, id := range s.Engine.ActiveIDs() {
			if !want[id] {
				s.Engine.StopChannel(id)
			}
		}
	}
	go s.holdOnce(context.Background())
	writeJSON(w, http.StatusOK, s.Snapshot())
}

func (s *Server) drain(w http.ResponseWriter, _ *http.Request) {
	s.State.Drain()
	s.Engine.StopAll()
	writeJSON(w, http.StatusOK, s.Snapshot())
}

// holdOnce starts (or refreshes) every held channel.
func (s *Server) holdOnce(ctx context.Context) {
	if ok, _ := s.State.MayRun(); !ok {
		return
	}
	for _, id := range s.State.Holds(s.now()) {
		hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := s.Engine.Hold(hctx, id); err != nil && !IsNotRunnable(err) {
			s.log().Debug("tv slot: warm hold failed", "channel_id", id.String(), "err", err.Error(),
				"event", "tv_slot_hold_failed")
		}
		cancel()
	}
}

// Run beats and keeps the warm holds until ctx ends.
func (s *Server) Run(ctx context.Context) {
	_ = s.BeatOnce(ctx)
	beat := time.NewTicker(BeatInterval)
	defer beat.Stop()
	hold := time.NewTicker(holdInterval)
	defer hold.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			_ = s.BeatOnce(ctx)
		case <-hold.C:
			s.holdOnce(ctx)
		}
	}
}

// BeatOnce writes the slot's tv_slots row and adopts the role and release
// binding it answers.
func (s *Server) BeatOnce(ctx context.Context) error {
	if s.Pool == nil {
		return nil
	}
	st := s.Snapshot()
	state := "healthy"
	if st.Mismatch {
		state = "unhealthy"
	}
	info := map[string]any{"state": state, "base_url": s.BaseURL, "pid": st.PID, "version": s.Version,
		"warm": st.Warm, "encoders_used": st.EncodersUsed, "started_at": st.StartedAt}
	b, _ := json.Marshal(info)
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var role string
	var epoch int64
	var release *string
	err := s.Pool.QueryRow(qctx, `SELECT role, role_epoch, release_id::text FROM tv_slot_beat($1, $2, $3::jsonb)`,
		s.HostID, s.State.Name, string(b)).Scan(&role, &epoch, &release)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("tv slot: heartbeat failed", "err", err.Error(), "event", "tv_slot_beat_failed")
		}
		return err
	}
	if s.State.SetRole(role, epoch) {
		s.log().Info("tv slot: role", "role", role, "epoch", epoch, "event", "tv_slot_role")
	}
	if release != nil {
		s.State.SetRelease(*release)
	} else {
		s.State.SetRelease("")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
