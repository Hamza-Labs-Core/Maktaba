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
	// OnBudget applies a budget the front door sent to the encoder gates at once
	// (nil: at each station's next start only): the front door lowers the NVENC
	// budget by the sessions VOD takes before it takes them.
	OnBudget func(ctx context.Context, b Budget)
	// Now (tests).
	Now func() time.Time
	// Bind pins an unbound slot to a release (POST /internal/tv/slot/bind;
	// nil: not offered).
	Bind func(ctx context.Context, release string) error

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
	r.Post("/internal/tv/slot/bind", s.bind)
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
	// Channels: what each station airs now (story 33.5: block, position,
	// encoder, restarts), for the staging verification.
	Channels []StationInfo `json:"channels,omitempty"`
	// The fixed stream layout (story 33.4 AC3).
	PMTPID    int `json:"pmt_pid"`
	VideoPID  int `json:"video_pid"`
	AudioPID  int `json:"audio_pid"`
	ServiceID int `json:"service_id"`
}

// StationInfo is one station in the status.
type StationInfo struct {
	ID             string     `json:"id"`
	Encoder        string     `json:"encoder,omitempty"`
	Restarts       int        `json:"restarts"`
	NVENCFallbacks int64      `json:"nvenc_fallbacks"`
	Reached        *time.Time `json:"reached,omitempty"`
	Block          *BlockInfo `json:"block,omitempty"`
}

// BlockInfo is the segment a station airs now: the schedule block (seq, video,
// start, end, title) or a synthetic slate (gap, empty schedule, failure).
type BlockInfo struct {
	Seq       int64     `json:"seq"`
	Kind      string    `json:"kind"`
	VideoID   string    `json:"video_id,omitempty"`
	StartAt   time.Time `json:"start_at"`
	EndAt     time.Time `json:"end_at"`
	Title     string    `json:"title,omitempty"`
	Synthetic bool      `json:"synthetic,omitempty"`
	// StandInFor: the seq of the schedule block this one airs in place of, its
	// file unreadable on this host (a filler or a slate); 0: none.
	StandInFor int64 `json:"stand_in_for,omitempty"`
}

// StatsSource is an engine that reports per-station stats (*channel.Engine).
type StatsSource interface {
	Stats(id uuid.UUID) (channel.StationStats, bool)
}

func stationInfo(id uuid.UUID, st channel.StationStats) StationInfo {
	si := StationInfo{ID: id.String(), Encoder: st.Encoder, Restarts: st.Restarts, NVENCFallbacks: st.NVENCFallbacks}
	if !st.Reached.IsZero() {
		r := st.Reached.UTC()
		si.Reached = &r
	}
	if seg := st.OnAir; seg != nil {
		b := &BlockInfo{Seq: seg.Block.Seq, Kind: seg.Kind, StartAt: seg.Block.StartAt.UTC(),
			EndAt: seg.Block.EndAt.UTC(), Title: seg.Block.Info.Title, Synthetic: seg.Synthetic,
			StandInFor: seg.StandInFor}
		if seg.Block.VideoID != uuid.Nil {
			b.VideoID = seg.Block.VideoID.String()
		}
		if seg.Synthetic {
			b.StartAt, b.EndAt = seg.Start.UTC(), seg.End.UTC()
		}
		si.Block = b
	}
	return si
}

// Snapshot is the slot's status.
func (s *Server) Snapshot() Status {
	role, epoch := s.State.Role()
	st := Status{Slot: s.State.Name, Role: role, Epoch: epoch, HostID: s.HostID, Release: s.State.ReleaseID(),
		Mismatch: s.State.Mismatch(), Version: s.Version, PID: os.Getpid(),
		StartedAt: s.started.UTC().Format(time.RFC3339), Budget: s.State.Budget(),
		PMTPID: channel.TSPMTPID, VideoPID: channel.TSVideoPID, AudioPID: channel.TSAudioPID,
		ServiceID: channel.TSServiceID, Stations: []string{}, Warm: []string{}}
	src, _ := s.Engine.(StatsSource)
	for _, id := range s.Engine.ActiveIDs() {
		st.Stations = append(st.Stations, id.String())
		if src != nil {
			if ss, ok := src.Stats(id); ok {
				st.Channels = append(st.Channels, stationInfo(id, ss))
			}
		}
	}
	sort.Slice(st.Channels, func(i, j int) bool { return st.Channels[i].ID < st.Channels[j].ID })
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
	if s.OnBudget != nil {
		s.OnBudget(r.Context(), req.Budget)
	}
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

// BindRequest is POST /internal/tv/slot/bind: the release an unbound slot airs
// from now on (its definitions, generation and settings).
type BindRequest struct {
	ReleaseID string `json:"release_id"`
}

func (s *Server) bind(w http.ResponseWriter, r *http.Request) {
	var req BindRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.ReleaseID == "" {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if cur := s.State.ReleaseID(); cur != "" {
		if cur != req.ReleaseID {
			http.Error(w, "the slot airs release "+cur, http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusOK, s.Snapshot())
		return
	}
	if s.Bind == nil {
		http.Error(w, "binding not offered", http.StatusNotImplemented)
		return
	}
	if err := s.Bind(r.Context(), req.ReleaseID); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	s.log().Info("tv slot: bound to the live release", "release_id", req.ReleaseID, "event", "tv_slot_release")
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
