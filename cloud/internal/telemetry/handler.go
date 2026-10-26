package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit"
)

// Ingester is the store the handler writes to.
type Ingester interface {
	Ingest(ctx context.Context, batches []Batch, now time.Time) error
}

// Handler serves POST /v1/telemetry.
type Handler struct {
	Store  Ingester
	Logger *slog.Logger
	// PerIP limits envelopes per client address (the address is read only
	// for this and never kept). PerInstall limits batches per install id.
	PerIP      *ratelimit.Limiter
	PerInstall *ratelimit.Limiter
	Now        func() time.Time
}

// Default limits: a home sends one report a day and forwards its apps'
// batches (each app flushes about once a minute); several homes can share
// an address behind a NAT.
const (
	DefaultIPBurst      = 120
	DefaultIPRate       = 2.0 // per second
	DefaultInstallBurst = 30
	DefaultInstallRate  = 1.0 / 20
)

// NewHandler wires a handler with the default limits.
func NewHandler(store Ingester, logger *slog.Logger) *Handler {
	return &Handler{
		Store: store, Logger: logger,
		PerIP:      ratelimit.NewLimiter(DefaultIPBurst, DefaultIPRate),
		PerInstall: ratelimit.NewLimiter(DefaultInstallBurst, DefaultInstallRate),
	}
}

// Mount registers the public ingest route (no authentication).
func Mount(r chi.Router, h *Handler) {
	r.Post("/v1/telemetry", h.ServeHTTP)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.PerIP != nil && !h.PerIP.Allow(ratelimit.IPKey(r)) {
		tooMany(w, h.PerIP)
		return
	}
	if r.ContentLength > MaxBody {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "body over 512 KiB")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "body over 512 KiB")
			return
		}
		writeErr(w, http.StatusBadRequest, "bad_request", "unreadable body")
		return
	}
	now := h.now()
	batches, err := Parse(body, now)
	if err != nil {
		if errors.Is(err, ErrTooManyBatches) {
			writeErr(w, http.StatusRequestEntityTooLarge, "too_many_batches", err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if h.PerInstall != nil {
		for _, b := range batches {
			// "delete what this app sent" is never refused
			if b.Type != "forget" && !h.PerInstall.Allow(b.Type+":"+b.InstallID) {
				tooMany(w, h.PerInstall)
				return
			}
		}
	}
	if err := h.Store.Ingest(r.Context(), batches, now); err != nil {
		if h.Logger != nil {
			h.Logger.Error("telemetry: ingest", "err", err)
		}
		writeErr(w, http.StatusInternalServerError, "internal", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func tooMany(w http.ResponseWriter, l *ratelimit.Limiter) {
	w.Header().Set("Retry-After", strconv.Itoa(l.RetryAfter()))
	writeErr(w, http.StatusTooManyRequests, "rate_limited", "")
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": kind, "message": msg})
}

// RunSweeper deletes rows past retention once at start and then daily,
// until ctx ends.
func RunSweeper(ctx context.Context, s *Store, ret Retention, logger *slog.Logger) {
	sweep := func() {
		res, err := s.Sweep(ctx, ret, time.Now())
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("telemetry: retention sweep", "err", err)
			}
			return
		}
		logger.Info("telemetry: retention sweep", "server_reports", res.ServerReports, "app_installs", res.AppInstalls,
			"app_events", res.AppEvents, "error_occurrences", res.ErrorOccurrences, "error_groups", res.ErrorGroups,
			"vitals", res.Vitals)
	}
	sweep()
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}
