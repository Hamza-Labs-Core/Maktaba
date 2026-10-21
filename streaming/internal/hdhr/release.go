package hdhr

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The guide of a staged TV release (Epic 33 story 33.6, AC3): its lineup
// (tv_release_channels) and its schedule generation, so a preview player (VLC
// with the staging M3U) browses what the change would put on the air. There is
// no staging HDHomeRun device: Plex and the SSDP announcement only ever see the
// live lineup.

// ReleaseGuide reads release's enabled channels and the programmes of its
// generation overlapping [start, end), ordered like the live guide.
func ReleaseGuide(ctx context.Context, pool *pgxpool.Pool, release uuid.UUID, start, end time.Time) ([]GuideChannel, []GuideProgram, error) {
	var gen int64
	if err := pool.QueryRow(ctx, `SELECT sched_gen FROM tv_releases WHERE id = $1`, release).Scan(&gen); err != nil {
		return nil, nil, err
	}
	rows, err := pool.Query(ctx, `SELECT channel_id, def FROM tv_release_channels WHERE release_id = $1`, release)
	if err != nil {
		return nil, nil, err
	}
	type def struct {
		Number    int     `json:"number"`
		Name      string  `json:"name"`
		Slug      string  `json:"slug"`
		LogoPath  *string `json:"logo_path"`
		Category  string  `json:"category"`
		SortOrder int     `json:"sort_order"`
		Enabled   *bool   `json:"enabled"`
	}
	type row struct {
		c    GuideChannel
		sort int
	}
	var list []row
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, nil, err
		}
		var d def
		if json.Unmarshal(raw, &d) != nil || (d.Enabled != nil && !*d.Enabled) {
			continue
		}
		c := GuideChannel{ID: id, Number: d.Number, Name: d.Name, Slug: d.Slug, Category: d.Category}
		if d.LogoPath != nil {
			c.LogoPath = *d.LogoPath
		}
		list = append(list, row{c, d.SortOrder})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].sort != list[j].sort {
			return list[i].sort < list[j].sort
		}
		return list[i].c.Number < list[j].c.Number
	})
	chans := make([]GuideChannel, 0, len(list))
	ids := make([]uuid.UUID, 0, len(list))
	for _, r := range list {
		chans = append(chans, r.c)
		ids = append(ids, r.c.ID)
	}
	prow, err := pool.Query(ctx, `SELECT channel_id, kind, start_at, end_at, title_snapshot, video_id FROM channel_programs
		WHERE sched_gen = $1 AND channel_id = ANY($2) AND start_at < $4 AND end_at > $3
		ORDER BY channel_id ASC, start_at ASC`, gen, ids, start, end)
	if err != nil {
		return nil, nil, err
	}
	defer prow.Close()
	var progs []GuideProgram
	for prow.Next() {
		var p GuideProgram
		var vid *uuid.UUID
		if err := prow.Scan(&p.ChannelID, &p.Kind, &p.StartAt, &p.EndAt, &p.Snapshot, &vid); err != nil {
			return nil, nil, err
		}
		if vid != nil {
			p.VideoID = *vid
		}
		progs = append(progs, p)
	}
	return chans, progs, prow.Err()
}

// BuildM3UWith is BuildM3U with the stream and guide URLs given (the staging
// preview's signed URLs); the logos are base's logo route.
func BuildM3UWith(base, xmltv string, chans []GuideChannel, stream func(GuideChannel) string) string {
	var b strings.Builder
	b.WriteString(`#EXTM3U url-tvg="` + xmltv + `" x-tvg-url="` + xmltv + `"` + "\n")
	for _, c := range chans {
		num := strconv.Itoa(c.Number)
		b.WriteString(`#EXTINF:-1 tvg-id="` + num + `" tvg-chno="` + num + `" tvg-name="` + m3uAttr(c.Name) + `"`)
		if icon := logoURL(base, c); icon != "" {
			b.WriteString(` tvg-logo="` + m3uAttr(icon) + `"`)
		}
		if c.Category != "" {
			b.WriteString(` group-title="` + m3uAttr(c.Category) + `"`)
		}
		b.WriteString("," + strings.NewReplacer("\n", " ", "\r", "").Replace(c.Name) + "\n")
		b.WriteString(stream(c) + "\n")
	}
	return b.String()
}

// BaseURL is scheme://host of a request (the trust rules of the guide's).
func BaseURL(configured string, r *http.Request) string { return publicBase(configured, r) }
