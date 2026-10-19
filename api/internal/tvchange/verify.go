package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Check results.
const (
	ResultPass = "pass"
	ResultWarn = "warn"
	ResultFail = "fail"
	ResultSkip = "skip"
)

// Check is one line of a verification report (design §6.2).
type Check struct {
	Check     string `json:"check"`
	ChannelID string `json:"channel_id,omitempty"`
	Result    string `json:"result"`
	Value     string `json:"value,omitempty"`
}

// releaseChannel is a tv_release_channels row as the checks read it.
type releaseChannel struct {
	ID           string
	Number       int
	Slug         string
	Name         string
	Enabled      bool
	Logo         string
	SchedChanged bool
}

func (c *Controller) releaseChannels(ctx context.Context, releaseID string) ([]releaseChannel, int64, error) {
	var gen int64
	if err := c.DB.QueryRowContext(ctx, `SELECT sched_gen FROM tv_releases WHERE id = $1`, releaseID).Scan(&gen); err != nil {
		return nil, 0, err
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT channel_id, def, sched_changed FROM tv_release_channels
		WHERE release_id = $1`, releaseID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []releaseChannel{}
	for rows.Next() {
		var id string
		var raw []byte
		var sched any
		if err := rows.Scan(&id, &raw, &sched); err != nil {
			return nil, 0, err
		}
		var d map[string]any
		_ = json.Unmarshal(raw, &d)
		rc := releaseChannel{ID: id, Enabled: true}
		if n, ok := d["number"].(float64); ok {
			rc.Number = int(n)
		}
		rc.Slug, _ = d["slug"].(string)
		rc.Name, _ = d["name"].(string)
		if e, ok := d["enabled"].(bool); ok {
			rc.Enabled = e
		}
		rc.Logo, _ = d["logo_path"].(string)
		switch v := sched.(type) {
		case bool:
			rc.SchedChanged = v
		case int64:
			rc.SchedChanged = v != 0
		}
		out = append(out, rc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, gen, rows.Err()
}

type block struct {
	video sql.NullString
	start nullTime
	end   nullTime
	kind  string
}

func (c *Controller) blockAt(ctx context.Context, channelID string, gen int64, at time.Time) (*block, error) {
	var b block
	err := c.DB.QueryRowContext(ctx, `SELECT video_id, start_at, end_at, kind FROM channel_programs
		WHERE channel_id = $1 AND sched_gen = $2 AND start_at <= $3 AND end_at > $3
		ORDER BY start_at DESC LIMIT 1`, channelID, gen, tsParam(c.DB, at)).Scan(&b.video, &b.start, &b.end, &b.kind)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// tsParam is a timestamp compared against channel_programs: native on Postgres; on
// SQLite the text form Python's sqlite3 adapter writes (what the scheduler stores).
func tsParam(db *sql.DB, t time.Time) any {
	if IsSQLite(db) {
		return t.UTC().Format("2006-01-02 15:04:05.000000+00:00")
	}
	return t.UTC()
}

// coverage is the end of the contiguous run of blocks of gen from at (zero when
// nothing is on air at at) and the gaps found.
func (c *Controller) coverage(ctx context.Context, channelID string, gen int64, at, until time.Time) (time.Time, int, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT start_at, end_at FROM channel_programs
		WHERE channel_id = $1 AND sched_gen = $2 AND end_at > $3 AND start_at < $4 ORDER BY start_at`,
		channelID, gen, tsParam(c.DB, at), tsParam(c.DB, until))
	if err != nil {
		return time.Time{}, 0, err
	}
	defer rows.Close()
	var end time.Time
	gaps := 0
	first := true
	for rows.Next() {
		var s, e nullTime
		if err := rows.Scan(&s, &e); err != nil {
			return time.Time{}, 0, err
		}
		if first {
			first = false
			if s.T.After(at) {
				gaps++
			}
		} else if s.T.Sub(end) > time.Second || end.Sub(s.T) > time.Second {
			gaps++
		}
		end = e.T
	}
	return end, gaps, rows.Err()
}

// Verify runs the checks the database can answer (the whole verification without a
// staging slot runtime; with one, streamverify.go adds the stream checks): lineup sanity, the new generation's
// guide coverage, and the invariant that makes every swap cut-free - the block on air
// now is the same in the new generation as in the live one (design §4.3).
func (c *Controller) Verify(ctx context.Context, ch *Change) (map[string]any, bool, int, error) {
	now := c.now()
	why := "no staging slot runtime: verified on the database"
	if c.interimWhy != "" {
		why = "no staging slot (" + c.interimWhy + "): verified on the database"
	}
	checks := []Check{{Check: "slot.health", Result: ResultSkip, Value: why}}
	failed, warnings := false, 0
	add := func(k Check) {
		checks = append(checks, k)
		switch k.Result {
		case ResultFail:
			failed = true
		case ResultWarn:
			warnings++
		}
	}
	if ch.ReleaseID == nil {
		return nil, true, 0, fmt.Errorf("change has no release")
	}
	chans, gen, err := c.releaseChannels(ctx, *ch.ReleaseID)
	if err != nil {
		return nil, true, 0, err
	}
	var live int64
	_ = c.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT live_gen FROM tv_routing WHERE scope = 'cluster'), 0)`).Scan(&live)
	numbers, slugs := map[int]string{}, map[string]string{}
	for _, rc := range chans {
		if !rc.Enabled {
			continue
		}
		if o, dup := numbers[rc.Number]; dup {
			add(Check{Check: "lineup.sanity", ChannelID: rc.ID, Result: ResultFail, Value: fmt.Sprintf("number %d also on %s", rc.Number, o)})
		}
		numbers[rc.Number] = rc.ID
		if o, dup := slugs[rc.Slug]; dup && rc.Slug != "" {
			add(Check{Check: "lineup.sanity", ChannelID: rc.ID, Result: ResultFail, Value: "slug " + rc.Slug + " also on " + o})
		}
		slugs[rc.Slug] = rc.ID
		if rc.Logo == "" {
			add(Check{Check: "lineup.sanity", ChannelID: rc.ID, Result: ResultWarn, Value: "no logo"})
		}
		end, gaps, err := c.coverage(ctx, rc.ID, gen, now, now.Add(24*time.Hour))
		if err != nil {
			return nil, true, 0, err
		}
		switch {
		case end.IsZero():
			add(Check{Check: "guide.coverage", ChannelID: rc.ID, Result: ResultFail, Value: "no programmes"})
		case gaps > 0:
			add(Check{Check: "guide.coverage", ChannelID: rc.ID, Result: ResultFail, Value: fmt.Sprintf("%d gaps in the next 24 h", gaps)})
		case end.Before(now.Add(24 * time.Hour)):
			add(Check{Check: "guide.coverage", ChannelID: rc.ID, Result: ResultWarn, Value: "covered until " + end.Format(time.RFC3339)})
		default:
			add(Check{Check: "guide.coverage", ChannelID: rc.ID, Result: ResultPass, Value: "24 h covered"})
		}
		lb, err := c.blockAt(ctx, rc.ID, live, now)
		if err != nil {
			return nil, true, 0, err
		}
		if lb == nil {
			continue // a channel not on the air now (an addition)
		}
		nb, err := c.blockAt(ctx, rc.ID, gen, now)
		if err != nil {
			return nil, true, 0, err
		}
		same := nb != nil && nb.video == lb.video && nb.start.T.Equal(lb.start.T) && nb.end.T.Equal(lb.end.T)
		if same {
			add(Check{Check: "channel.block", ChannelID: rc.ID, Result: ResultPass, Value: "the programme on the air is kept"})
		} else {
			add(Check{Check: "channel.block", ChannelID: rc.ID, Result: ResultFail, Value: "the new schedule would cut the programme on the air"})
		}
	}
	report := map[string]any{
		"checks":   checks,
		"passed":   !failed,
		"warnings": warnings,
		"at":       now.Format(time.RFC3339),
		"interim":  true,
	}
	return report, failed, warnings, nil
}

func (c *Controller) verifyAndMove(ctx context.Context, ch *Change) error {
	report, failed, _, err := c.Verify(ctx, ch)
	if err != nil {
		report = map[string]any{"error": err.Error(), "passed": false, "at": c.now().Format(time.RFC3339)}
		failed = true
	}
	if err := c.Store.Set(ctx, c.DB, ch.ID, map[string]any{"verify_report": report}); err != nil {
		return err
	}
	ch.VerifyReport = report
	if failed {
		return c.Store.Move(ctx, c.DB, ch, StateFailed, ActionFail, nil, map[string]any{"report": "failed checks"},
			map[string]any{"ended_at": c.now(), "end_reason": "verification_failed"})
	}
	return c.Store.Move(ctx, c.DB, ch, StateReady, ActionPass, nil, nil, map[string]any{"ready_at": c.now()})
}
