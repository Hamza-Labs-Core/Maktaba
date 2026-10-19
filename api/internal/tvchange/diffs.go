package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// What the Changes page reads beside the change itself (story 33.11): the programmes
// of two generations side by side, and when each changed channel is expected to switch.

// Programme is one channel_programs block as the schedule diff shows it.
type Programme struct {
	StartAt time.Time `json:"start_at"`
	EndAt   time.Time `json:"end_at"`
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`
	VideoID *string   `json:"video_id,omitempty"`
}

func (s *Store) tsParam(t time.Time) any { return tsParam(s.DB, t) }

// Programmes are a channel's blocks of generation gen overlapping [from, to).
func (s *Store) Programmes(ctx context.Context, channelID string, gen int64, from, to time.Time) ([]Programme, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT start_at, end_at, kind, video_id, title_snapshot FROM channel_programs
		WHERE channel_id = $1 AND sched_gen = $2 AND end_at > $3 AND start_at < $4 ORDER BY start_at LIMIT 500`,
		channelID, gen, s.tsParam(from), s.tsParam(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Programme{}
	for rows.Next() {
		var p Programme
		var st, en nullTime
		var vid sql.NullString
		var snap []byte
		if err := rows.Scan(&st, &en, &p.Kind, &vid, &snap); err != nil {
			return nil, err
		}
		p.StartAt, p.EndAt, p.VideoID = st.T, en.T, strPtr(vid)
		var m map[string]any
		decodeJSON(snap, &m)
		p.Title, _ = m["title"].(string)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Gens are the live generation and the change's release generation (-1 when the
// change has no release yet).
func (s *Store) Gens(ctx context.Context, ch *Change) (live, staged int64, err error) {
	staged = -1
	if err = s.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT live_gen FROM tv_routing WHERE scope = 'cluster'), 0)`).
		Scan(&live); err != nil {
		return 0, 0, err
	}
	if ch.ReleaseID != nil {
		err = s.DB.QueryRowContext(ctx, `SELECT sched_gen FROM tv_releases WHERE id = $1`, *ch.ReleaseID).Scan(&staged)
		if errors.Is(err, sql.ErrNoRows) {
			return live, -1, nil
		}
	}
	return live, staged, err
}

// SameProgrammes reports whether two block lists air the same thing at the same times.
func SameProgrammes(a, b []Programme) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if !x.StartAt.Equal(y.StartAt) || !x.EndAt.Equal(y.EndAt) || x.Title != y.Title || x.Kind != y.Kind ||
			(x.VideoID == nil) != (y.VideoID == nil) || (x.VideoID != nil && *x.VideoID != *y.VideoID) {
			return false
		}
	}
	return true
}

// ScheduleChannel is one entry of diff.schedules (the pipeline's compute_diff).
type ScheduleChannel struct {
	ID           string     `json:"id"`
	Number       int        `json:"number"`
	Name         string     `json:"name"`
	Reason       string     `json:"reason"`
	DivergenceAt *time.Time `json:"divergence_at,omitempty"`
}

// ScheduleChannels are the channels whose schedule the change regenerates, by number.
func (c *Change) ScheduleChannels() []ScheduleChannel {
	raw, _ := json.Marshal(c.Diff["schedules"])
	var items []struct {
		ID           string  `json:"id"`
		Number       float64 `json:"number"`
		Name         string  `json:"name"`
		Reason       string  `json:"reason"`
		DivergenceAt string  `json:"divergence_at"`
	}
	_ = json.Unmarshal(raw, &items)
	out := make([]ScheduleChannel, 0, len(items))
	for _, it := range items {
		sc := ScheduleChannel{ID: it.ID, Number: int(it.Number), Name: it.Name, Reason: it.Reason}
		var t nullTime
		if t.parse(it.DivergenceAt) == nil && t.Valid {
			sc.DivergenceAt = t.ptr()
		}
		out = append(out, sc)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// Boundary is when one changed channel is expected to switch to the new schedule.
type Boundary struct {
	ChannelID string    `json:"channel_id"`
	Number    int       `json:"number"`
	Name      string    `json:"name"`
	SwitchAt  time.Time `json:"switch_at"`
	// OnAir is the title of the programme it waits for ("" when nothing is on the air).
	OnAir string `json:"on_air,omitempty"`
	// Capped: the programme runs past boundary_max_wait_minutes; it switches then.
	Capped bool `json:"capped,omitempty"`
}

// Boundaries are the expected per-channel switch times of a swap at `at` (design 4.3,
// README swap_wait): each changed channel switches at the end of the programme on the
// air at `at` in the live generation, at most maxWait later; immediate: at `at`.
func (s *Store) Boundaries(ctx context.Context, ch *Change, at time.Time, wait string, maxWait time.Duration) ([]Boundary, error) {
	live, _, err := s.Gens(ctx, ch)
	if err != nil {
		return nil, err
	}
	out := []Boundary{}
	for _, sc := range ch.ScheduleChannels() {
		b := Boundary{ChannelID: sc.ID, Number: sc.Number, Name: sc.Name, SwitchAt: at}
		if wait != "immediate" {
			ps, err := s.Programmes(ctx, sc.ID, live, at, at.Add(time.Second))
			if err != nil {
				return nil, err
			}
			if len(ps) > 0 {
				p := ps[0]
				b.OnAir = p.Title
				b.SwitchAt = p.EndAt
				if maxWait > 0 && p.EndAt.Sub(at) > maxWait {
					b.SwitchAt, b.Capped = at.Add(maxWait), true
				}
			}
		}
		out = append(out, b)
	}
	return out, nil
}
