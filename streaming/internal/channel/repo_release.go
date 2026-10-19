package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReleaseRepo is the Repo of a TV slot bound to one TV release (Epic 33, story 33.2
// AC3; design §2.3): channel definitions come from tv_release_channels(release),
// programmes from channel_programs of the release's schedule generation only, and the
// settings from tv_releases.settings - editing channels or app_settings never changes
// what a running slot airs. The runtime rows (channel_runtime) are the PGRepo's.
//
// The two-slot runtime (story 33.1, cmd_slot.go) builds one with NewReleaseRepo when
// MAKTABA_TV_RELEASE is set; the single-process engine keeps PGRepo, which reads the
// live generation (channel_programs_live).
type ReleaseRepo struct {
	*PGRepo
	ReleaseID uuid.UUID
	SchedGen  int64
	defs      map[uuid.UUID]releaseDef
	settings  map[string]json.RawMessage
}

type releaseDef struct {
	Name       string          `json:"name"`
	LogoPath   *string         `json:"logo_path"`
	Enabled    *bool           `json:"enabled"`
	ModeConfig json.RawMessage `json:"mode_config"`
}

// ErrNoRelease is a release id the database does not have.
var ErrNoRelease = errors.New("channel: no such TV release")

// NewReleaseRepo loads release id's generation, definitions and settings.
func NewReleaseRepo(ctx context.Context, pool *pgxpool.Pool, host string, id uuid.UUID) (*ReleaseRepo, error) {
	r := &ReleaseRepo{PGRepo: NewPGRepo(pool, host), ReleaseID: id, defs: map[uuid.UUID]releaseDef{}}
	var settings []byte
	err := pool.QueryRow(ctx, `SELECT sched_gen, settings FROM tv_releases WHERE id = $1`, id).Scan(&r.SchedGen, &settings)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrNoRelease, id)
	}
	if err != nil {
		return nil, err
	}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &r.settings)
	}
	rows, err := pool.Query(ctx, `SELECT channel_id, def FROM tv_release_channels WHERE release_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid uuid.UUID
		var raw []byte
		if err := rows.Scan(&cid, &raw); err != nil {
			return nil, err
		}
		var d releaseDef
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("release %s channel %s: %w", id, cid, err)
		}
		r.defs[cid] = d
	}
	return r, rows.Err()
}

// Settings is a rules section of the release ("tv", "tv_output"): its stored overrides
// (an empty object when the release keeps the defaults).
func (r *ReleaseRepo) Settings(section string) json.RawMessage {
	if v, ok := r.settings[section]; ok && len(v) > 0 {
		return v
	}
	return json.RawMessage(`{}`)
}

// ProgramsFrom reads the release's generation (never the live view).
func (r *ReleaseRepo) ProgramsFrom(ctx context.Context, channelID uuid.UUID, from time.Time, limit int) ([]ProgramBlock, error) {
	srcCol := "NULL::text"
	if r.sourceURLColumn(ctx) {
		srcCol = "cp.source_url"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT cp.seq, cp.kind, cp.video_id, cp.start_at, cp.end_at,
		       cp.source_offset, cp.source_duration, v.path, `+srcCol+`, cp.title_snapshot
		FROM channel_programs cp
		LEFT JOIN videos v ON v.id = cp.video_id
		WHERE cp.channel_id = $1 AND cp.sched_gen = $4 AND cp.end_at > $2
		ORDER BY cp.start_at ASC
		LIMIT $3
	`, channelID, from, limit, r.SchedGen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProgramBlock
	for rows.Next() {
		var b ProgramBlock
		var vid *uuid.UUID
		var path, src *string
		var snap []byte
		if err := rows.Scan(&b.Seq, &b.Kind, &vid, &b.StartAt, &b.EndAt,
			&b.SourceOffsetMS, &b.SourceDurationMS, &path, &src, &snap); err != nil {
			return nil, err
		}
		if vid != nil {
			b.VideoID = *vid
		}
		if path != nil {
			b.Path = *path
		}
		if src != nil {
			b.SourceURL = *src
		}
		b.Info = ParseBlockInfo(snap)
		out = append(out, b)
	}
	return out, rows.Err()
}

// Look is the channel's look as the release defines it. ErrChannelNotFound for a
// channel the release does not have or has disabled.
func (r *ReleaseRepo) Look(_ context.Context, channelID uuid.UUID) (Look, error) {
	d, ok := r.defs[channelID]
	if !ok || (d.Enabled != nil && !*d.Enabled) {
		return Look{}, ErrChannelNotFound
	}
	var mc struct {
		Look json.RawMessage `json:"look"`
	}
	if len(d.ModeConfig) > 0 {
		_ = json.Unmarshal(d.ModeConfig, &mc)
	}
	l := ParseLook(mc.Look, deref(d.LogoPath))
	l.Name = d.Name
	return l, nil
}

// Channels are the release's channel ids.
func (r *ReleaseRepo) Channels() []uuid.UUID {
	out := make([]uuid.UUID, 0, len(r.defs))
	for id := range r.defs {
		out = append(out, id)
	}
	return out
}

var _ Repo = (*ReleaseRepo)(nil)
