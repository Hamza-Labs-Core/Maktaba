package main

import (
	"log/slog"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/liveguard"
)

// liveTVGuard is the home's live TV guard (Epic 57 story 57.10): signed channel URLs
// verified with the machine's keys, the parental verdict asked of this machine's API
// with the local token, the tuner's allowed addresses read from the database. Without
// an API to ask (no supervisor), a named viewer is refused: said once at start.
func liveTVGuard(logger *slog.Logger, cfg config.Config, keys *auth.JWKSCache, pool *pgxpool.Pool) *liveguard.Guard {
	verdicts := liveguard.APIVerdictsFromEnv()
	if verdicts.BaseURL == "" || verdicts.TokenFile == "" {
		logger.Warn("tv: no API to ask the parental verdict (MAKTABA_LINK_PULL_URL, MAKTABA_SUPERVISOR_TOKEN_FILE); a signed-in viewer's channel stream is refused",
			"event", "tv_parental_no_api", "link_pull_url_set", os.Getenv("MAKTABA_LINK_PULL_URL") != "")
	}
	return &liveguard.Guard{
		Keys:     keys,
		Leeway:   cfg.LeewayDuration(),
		Verdicts: verdicts,
		Tuner:    &liveguard.Tuner{Load: liveguard.PGTunerLoad(pool, nil), Logger: logger},
		Logger:   logger,
	}
}

// guardMounts is every TV mount behind the guard.
func guardMounts(guard *liveguard.Guard, mounts []func(chi.Router)) []func(chi.Router) {
	out := make([]func(chi.Router), len(mounts))
	for i, mount := range mounts {
		out[i] = guard.Wrap(mount)
	}
	return out
}
