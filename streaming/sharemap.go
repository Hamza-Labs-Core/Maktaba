package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// Story 31.3: media paths are stored canonical (smb://<server>/<share>/...).
// The pipeline on this machine resolves them through its mounts and reports
// the result as instance_identity.storage.path_map (the home); this process
// installs that as the default share map, under MAKTABA_STREAM_PATH_MAP (which
// wins on an equal prefix), and re-reads it every few minutes.

const shareMapEvery = 5 * time.Minute

const homeShareMapSQL = `SELECT COALESCE(storage->'path_map', '{}'::jsonb)::text
	FROM instance_identity WHERE id = 1`

// homeShareMap reads the home's reported share map; an older schema or no row
// is an empty map.
func homeShareMap(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	var raw string
	if err := pool.QueryRow(ctx, homeShareMapSQL).Scan(&raw); err != nil {
		return nil, err
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// startShareMap installs the share map now and keeps it fresh until ctx ends.
func startShareMap(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	env := pathmap.FromEnv()
	pathmap.SetDefault(env)
	if pool == nil {
		return
	}
	load := func() {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		entries, err := homeShareMap(c, pool)
		if err != nil {
			logger.Debug("share map not read", "err", err, "event", "share_map_unavailable")
			return
		}
		pathmap.SetDefault(env.Merge(pathmap.FromEntries(entries)))
	}
	load()
	go func() {
		t := time.NewTicker(shareMapEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				load()
			}
		}
	}()
}
