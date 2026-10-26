package analytics

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// repo holds the aggregate reads. driver selects dialect-specific SQL for
// timestamps, date bucketing and DOW-HOUR extraction (Postgres in
// production; SQLite for the unified home-server binary).
type repo struct {
	db     *sql.DB
	driver string // "postgres" | "sqlite"
}

func newRepo(db *sql.DB, driver string) *repo {
	if driver == "" {
		driver = "postgres"
	}
	return &repo{db: db, driver: driver}
}

func (r *repo) isSQLite() bool { return r.driver == "sqlite" }

// ─── Summary ───────────────────────────────────────────────────────────

// summaryTotals fills the headline numbers.
func (r *repo) summaryTotals(ctx context.Context, f Filters, s *Summary) error {
	b := r.newQ()
	q := b.with(f) + `
	SELECT COALESCE(SUM(seconds),0),
	       COALESCE(SUM(CASE WHEN surface = 'video' THEN seconds ELSE 0 END),0),
	       COALESCE(SUM(CASE WHEN surface = 'video' THEN 1 ELSE 0 END),0),
	       COALESCE(SUM(CASE WHEN surface <> 'video' THEN 1 ELSE 0 END),0),
	       COUNT(DISTINCT user_id),
	       COALESCE(SUM(completed),0)
	FROM ev`
	var completed int64
	if err := r.db.QueryRowContext(ctx, q, b.args...).Scan(
		&s.WatchSec, &s.VideoSec, &s.Plays, &s.Viewings, &s.People, &completed); err != nil {
		return err
	}
	s.TVSec = s.WatchSec - s.VideoSec
	s.LiveTVHours = float64(s.TVSec) / 3600
	if s.Plays > 0 {
		s.CompletionRate = float64(completed) / float64(s.Plays)
	}
	return nil
}

// breakdown groups ev by a dimension: surface, app, platform or device.
// Empty and missing values read as "unknown".
func (r *repo) breakdown(ctx context.Context, f Filters, dim string) ([]CountStat, error) {
	col := map[string]string{
		"surface":  "surface",
		"app":      appExpr("client_app"),
		"platform": "COALESCE(NULLIF(platform,''),'unknown')",
		"device":   "COALESCE(NULLIF(device_type,''),'unknown')",
	}[dim]
	b := r.newQ()
	q := b.with(f) + `
	SELECT ` + col + `, COUNT(*), COALESCE(SUM(seconds),0), COUNT(DISTINCT user_id)
	FROM ev GROUP BY 1 ORDER BY 3 DESC, 1 ASC`
	return r.scanCountStats(ctx, q, b.args)
}

// surfaces is the breakdown by surface with every surface present, in a
// fixed order (want limits it, e.g. the TV surfaces only).
func (r *repo) surfaces(ctx context.Context, f Filters, want []string) ([]CountStat, error) {
	got, err := r.breakdown(ctx, f, "surface")
	if err != nil {
		return nil, err
	}
	by := map[string]CountStat{}
	for _, s := range got {
		by[s.Label] = s
	}
	out := make([]CountStat, 0, len(want))
	for _, s := range want {
		c := by[s]
		c.Label = s
		out = append(out, c)
	}
	return out, nil
}

func (r *repo) genres(ctx context.Context, f Filters, limit int) ([]CountStat, error) {
	b := r.newQ()
	q := b.with(f) + `
	SELECT t.name, COUNT(*), COALESCE(SUM(ev.seconds),0), COUNT(DISTINCT ev.user_id)
	FROM ev
	JOIN video_tags vt ON vt.video_id = ev.video_id
	JOIN tags t        ON t.id = vt.tag_id
	WHERE ev.surface = 'video'
	GROUP BY t.name ORDER BY 2 DESC, 1 ASC LIMIT ` + b.arg(limit)
	return r.scanCountStats(ctx, q, b.args)
}

func (r *repo) libraries(ctx context.Context, f Filters) ([]LabelStat, error) {
	b := r.newQ()
	q := b.with(f) + `
	SELECT l.id, l.name, COUNT(*), COALESCE(SUM(ev.seconds),0), COUNT(DISTINCT ev.user_id)
	FROM ev
	JOIN videos v    ON v.id = ev.video_id
	JOIN libraries l ON l.id = v.library_id
	WHERE ev.surface = 'video'
	GROUP BY l.id, l.name ORDER BY 4 DESC, 2 ASC`
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LabelStat{}
	for rows.Next() {
		var s LabelStat
		if err := rows.Scan(&s.ID, &s.Label, &s.Sessions, &s.WatchSec, &s.People); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *repo) scanCountStats(ctx context.Context, q string, args []any) ([]CountStat, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CountStat{}
	for rows.Next() {
		var s CountStat
		if err := rows.Scan(&s.Label, &s.Sessions, &s.WatchSec, &s.People); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── Now ───────────────────────────────────────────────────────────────

// nowPlays is every play with a heartbeat since playCutoff.
func (r *repo) nowPlays(ctx context.Context, f Filters, playCutoff, now time.Time) ([]NowPlay, error) {
	b := r.newQ()
	w := b.playWhere(f, false)
	cond := "ws.state = 'active' AND " + r.ts("ws.last_heartbeat") + " >= " + b.arg(r.tArg(playCutoff))
	if w == "" {
		w = " WHERE " + cond
	} else {
		w += " AND " + cond
	}
	q := `SELECT ws.id, CAST(ws.user_id AS TEXT), COALESCE(u.username,''), ws.video_id,
	             COALESCE(NULLIF(v.title,''), v.filename), ws.started_at, ws.percent_complete,
	             ws.client_app, ws.client_version, ws.platform, ws.device_type
	      FROM watch_sessions ws
	      JOIN videos v     ON v.id = ws.video_id
	      LEFT JOIN users u ON u.id = ws.user_id` + w + `
	      ORDER BY ` + r.ts("ws.started_at") + ` DESC LIMIT 200`
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NowPlay{}
	for rows.Next() {
		var p NowPlay
		var started dbTime
		var app, ver, platform, device nullString
		if err := rows.Scan(&p.SessionID, &p.UserID, &p.Username, &p.VideoID, &p.Title, &started,
			&p.PercentComplete, &app, &ver, &platform, &device); err != nil {
			return nil, err
		}
		p.StartedAt = started.String()
		p.ElapsedSec = elapsed(started, now)
		p.ClientApp, p.ClientVersion, p.Platform, p.DeviceType = orUnknown(app.S), ver.S, platform.S, device.S
		out = append(out, p)
	}
	return out, rows.Err()
}

// nowViewings is every open viewing seen since its surface's cutoff (the
// remote TV's heartbeat runs on its own clock).
func (r *repo) nowViewings(ctx context.Context, f Filters, tvCutoff, remoteCutoff, now time.Time) ([]NowViewing, error) {
	b := r.newQ()
	w := b.viewWhere(f, false)
	cond := "tvv.ended_at IS NULL AND ((tvv.surface = 'remote' AND " + r.ts("tvv.last_seen_at") + " >= " +
		b.arg(r.tArg(remoteCutoff)) + ") OR (tvv.surface <> 'remote' AND " + r.ts("tvv.last_seen_at") +
		" >= " + b.arg(r.tArg(tvCutoff)) + "))"
	if w == "" {
		w = " WHERE " + cond
	} else {
		w += " AND " + cond
	}
	q := `WITH ` + b.tvvCTE() + `
	      SELECT tvv.id, tvv.surface, tvv.channel_id, c.name, c.number, tvv.user_id,
	             COALESCE(u.username,''), tvv.started_at, tvv.client_app, tvv.client_version,
	             tvv.platform, tvv.device_type
	      FROM tvv
	      JOIN channels c   ON c.id = tvv.channel_id
	      LEFT JOIN users u ON CAST(u.id AS TEXT) = tvv.user_id` + w + `
	      ORDER BY ` + r.ts("tvv.started_at") + ` DESC LIMIT 200`
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NowViewing{}
	for rows.Next() {
		var v NowViewing
		var started dbTime
		var user, app, ver, platform, device nullString
		if err := rows.Scan(&v.ID, &v.Surface, &v.ChannelID, &v.ChannelName, &v.ChannelNumber, &user,
			&v.Username, &started, &app, &ver, &platform, &device); err != nil {
			return nil, err
		}
		v.UserID = user.S
		v.StartedAt = started.String()
		v.ElapsedSec = elapsed(started, now)
		v.ClientApp, v.ClientVersion, v.Platform, v.DeviceType = orUnknown(app.S), ver.S, platform.S, device.S
		out = append(out, v)
	}
	return out, rows.Err()
}

func elapsed(started dbTime, now time.Time) int {
	if !started.Valid {
		return 0
	}
	if s := int(now.Sub(started.T).Seconds()); s > 0 {
		return s
	}
	return 0
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// ─── Top videos ────────────────────────────────────────────────────────

func (r *repo) topVideos(ctx context.Context, f Filters, limit int) ([]TopVideo, error) {
	b := r.newQ()
	q := b.with(f) + `
	SELECT ev.video_id, COALESCE(NULLIF(v.title,''), v.filename), COUNT(*),
	       COALESCE(SUM(ev.seconds),0), COUNT(DISTINCT ev.user_id), COALESCE(AVG(ev.completed),0)
	FROM ev
	JOIN videos v ON v.id = ev.video_id
	WHERE ev.surface = 'video'
	GROUP BY ev.video_id, v.title, v.filename
	ORDER BY 3 DESC, 4 DESC, 2 ASC LIMIT ` + b.arg(limit)
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TopVideo{}
	for rows.Next() {
		var v TopVideo
		if err := rows.Scan(&v.VideoID, &v.Title, &v.Sessions, &v.WatchSec, &v.Viewers, &v.CompletionRate); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ─── Top channels ──────────────────────────────────────────────────────

// maxPeakRows bounds the viewings read to find the busiest moment.
const maxPeakRows = 100000

func (r *repo) topChannels(ctx context.Context, f Filters, limit int) (TopChannelsResponse, error) {
	out := TopChannelsResponse{Channels: []TopChannel{}}
	// Per channel and surface; the channel's totals are summed from these,
	// its distinct people counted on their own.
	b := r.newQ()
	q := b.with(f) + `
	SELECT CAST(ev.channel_id AS TEXT), c.name, c.number, ev.surface, COUNT(*),
	       COALESCE(SUM(ev.seconds),0), COUNT(DISTINCT ev.user_id)
	FROM ev
	JOIN channels c ON c.id = ev.channel_id
	WHERE ev.surface <> 'video'
	GROUP BY ev.channel_id, c.name, c.number, ev.surface`
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return out, err
	}
	by := map[string]*TopChannel{}
	for rows.Next() {
		var id, name, surface string
		var number int
		var s CountStat
		if err := rows.Scan(&id, &name, &number, &surface, &s.Sessions, &s.WatchSec, &s.People); err != nil {
			rows.Close()
			return out, err
		}
		c := by[id]
		if c == nil {
			c = &TopChannel{ChannelID: id, Name: name, Number: number, Surfaces: []CountStat{}}
			by[id] = c
		}
		s.Label = surface
		c.Surfaces = append(c.Surfaces, s)
		c.Viewings += s.Sessions
		c.WatchSec += s.WatchSec
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	b = r.newQ()
	q = b.with(f) + `
	SELECT CAST(channel_id AS TEXT), COUNT(DISTINCT user_id) FROM ev
	WHERE surface <> 'video' GROUP BY channel_id`
	rows, err = r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id string
		var people int64
		if err := rows.Scan(&id, &people); err != nil {
			rows.Close()
			return out, err
		}
		if c := by[id]; c != nil {
			c.People = people
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	// The busiest moment, per channel and across them: every viewing as an
	// interval, swept in time order.
	b = r.newQ()
	q = b.with(f) + `
	SELECT CAST(channel_id AS TEXT), started_at, last_at FROM ev
	WHERE surface <> 'video' LIMIT ` + b.arg(maxPeakRows)
	rows, err = r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return out, err
	}
	var all []interval
	per := map[string][]interval{}
	for rows.Next() {
		var id string
		var start, end dbTime
		if err := rows.Scan(&id, &start, &end); err != nil {
			rows.Close()
			return out, err
		}
		if !start.Valid {
			continue
		}
		iv := interval{start: start.T, end: end.T}
		if !end.Valid || iv.end.Before(iv.start) {
			iv.end = iv.start
		}
		all = append(all, iv)
		per[id] = append(per[id], iv)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	var at time.Time
	out.PeakViewers, at = peak(all)
	if out.PeakViewers > 0 {
		out.PeakAt = at.Format(time.RFC3339)
	}
	for id, c := range by {
		c.PeakViewers, _ = peak(per[id])
		sort.Slice(c.Surfaces, func(i, j int) bool { return c.Surfaces[i].WatchSec > c.Surfaces[j].WatchSec })
		out.Channels = append(out.Channels, *c)
	}
	sort.Slice(out.Channels, func(i, j int) bool {
		a, b := out.Channels[i], out.Channels[j]
		if a.WatchSec != b.WatchSec {
			return a.WatchSec > b.WatchSec
		}
		if a.Viewings != b.Viewings {
			return a.Viewings > b.Viewings
		}
		return a.Number < b.Number
	})
	if len(out.Channels) > limit {
		out.Channels = out.Channels[:limit]
	}
	return out, nil
}

// interval is one viewing's time, start to last seen.
type interval struct{ start, end time.Time }

// peak is the most intervals open at one moment, and the first moment it
// was reached. An interval that ends as another starts does not overlap it.
func peak(ivs []interval) (int, time.Time) {
	type edge struct {
		t     time.Time
		delta int
	}
	edges := make([]edge, 0, 2*len(ivs))
	for _, iv := range ivs {
		end := iv.end
		if !end.After(iv.start) {
			end = iv.start.Add(time.Nanosecond) // a viewing seen once still counts
		}
		edges = append(edges, edge{iv.start, 1}, edge{end, -1})
	}
	sort.Slice(edges, func(i, j int) bool {
		if !edges[i].t.Equal(edges[j].t) {
			return edges[i].t.Before(edges[j].t)
		}
		return edges[i].delta < edges[j].delta // an end at t closes before a start at t
	})
	cur, best := 0, 0
	var at time.Time
	for _, e := range edges {
		cur += e.delta
		if cur > best {
			best, at = cur, e.t
		}
	}
	return best, at
}

// ─── People ────────────────────────────────────────────────────────────

func (r *repo) people(ctx context.Context, f Filters, limit int) ([]Person, error) {
	b := r.newQ()
	q := b.with(f) + `
	SELECT ev.user_id, COALESCE(MAX(u.username),''), COALESCE(SUM(ev.seconds),0),
	       COALESCE(SUM(CASE WHEN ev.surface = 'video' THEN ev.seconds ELSE 0 END),0),
	       COALESCE(SUM(CASE WHEN ev.surface = 'video' THEN 1 ELSE 0 END),0),
	       COALESCE(SUM(CASE WHEN ev.surface <> 'video' THEN 1 ELSE 0 END),0),
	       COUNT(DISTINCT ev.channel_id), MAX(` + r.ts("ev.last_at") + `)
	FROM ev
	LEFT JOIN users u ON CAST(u.id AS TEXT) = ev.user_id
	WHERE ev.user_id IS NOT NULL
	GROUP BY ev.user_id`
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, err
	}
	by := map[string]*Person{}
	for rows.Next() {
		p := &Person{Apps: []string{}}
		var last dbTime
		if err := rows.Scan(&p.UserID, &p.Username, &p.WatchSec, &p.VideoSec, &p.Plays, &p.Viewings,
			&p.Channels, &last); err != nil {
			rows.Close()
			return nil, err
		}
		p.TVSec = p.WatchSec - p.VideoSec
		p.LastWatchedAt = last.String()
		by[p.UserID] = p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	apps := map[string]map[string]bool{}
	addApp := func(user, app string) {
		if apps[user] == nil {
			apps[user] = map[string]bool{}
		}
		apps[user][app] = true
	}
	b = r.newQ()
	q = b.with(f) + `SELECT DISTINCT user_id, ` + appExpr("client_app") + ` FROM ev WHERE user_id IS NOT NULL`
	if err := r.eachPair(ctx, q, b.args, addApp); err != nil {
		return nil, err
	}

	// Who used an app (the API's daily people), when the filters are about
	// people and apps rather than what was watched.
	if f.Library == "" && f.Surface == "" {
		b = r.newQ()
		conds := b.dayBounds(f, "a.day", nil)
		if f.User != "" {
			conds = append(conds, "CAST(a.user_id AS TEXT) = "+b.arg(f.User))
		}
		if f.ClientApp != "" {
			conds = append(conds, "a.client_app = "+b.arg(f.ClientApp))
		}
		q = `SELECT CAST(a.user_id AS TEXT), u.username, a.client_app, MAX(` + r.dayText("a.day") + `)
		     FROM api_usage_people_daily a JOIN users u ON u.id = a.user_id` + where(conds) + `
		     GROUP BY a.user_id, u.username, a.client_app`
		rows, err := r.db.QueryContext(ctx, q, b.args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var user, name, app, day string
			if err := rows.Scan(&user, &name, &app, &day); err != nil {
				rows.Close()
				return nil, err
			}
			p := by[user]
			if p == nil {
				p = &Person{UserID: user, Username: name, Apps: []string{}}
				by[user] = p
			}
			if day > p.LastActiveDay {
				p.LastActiveDay = day
			}
			addApp(user, app)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	out := make([]Person, 0, len(by))
	for id, p := range by {
		for a := range apps[id] {
			p.Apps = append(p.Apps, a)
		}
		sort.Strings(p.Apps)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.WatchSec != b.WatchSec {
			return a.WatchSec > b.WatchSec
		}
		if a.LastActiveDay != b.LastActiveDay {
			return a.LastActiveDay > b.LastActiveDay
		}
		return a.Username < b.Username
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// eachPair runs a two-text-column query and hands each row to fn.
func (r *repo) eachPair(ctx context.Context, q string, args []any, fn func(a, b string)) error {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return err
		}
		fn(a, b)
	}
	return rows.Err()
}

// ─── Apps ──────────────────────────────────────────────────────────────

// apps reads the API's use per app and platform (api_usage_daily), the
// people and versions per app (api_usage_people_daily) and the daily trend.
// The person filter narrows the people and versions only: the request
// counts hold no person.
func (r *repo) apps(ctx context.Context, f Filters) ([]AppUsage, []UsagePoint, error) {
	b := r.newQ()
	conds := b.dayBounds(f, "day", nil)
	if f.ClientApp != "" {
		conds = append(conds, "client_app = "+b.arg(f.ClientApp))
	}
	q := `SELECT client_app, platform, COALESCE(SUM(requests),0), COALESCE(SUM(errors),0),
	             COALESCE(SUM(total_ms),0), COALESCE(SUM(slow),0)
	      FROM api_usage_daily` + where(conds) + `
	      GROUP BY client_app, platform`
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, nil, err
	}
	by := map[string]*AppUsage{}
	totalMs := map[string]int64{}
	get := func(app string) *AppUsage {
		a := by[app]
		if a == nil {
			a = &AppUsage{ClientApp: app, Versions: []VersionUse{}, Platforms: []PlatformUsage{}}
			by[app] = a
		}
		return a
	}
	for rows.Next() {
		var app string
		var p PlatformUsage
		var ms int64
		if err := rows.Scan(&app, &p.Platform, &p.Requests, &p.Errors, &ms, &p.Slow); err != nil {
			rows.Close()
			return nil, nil, err
		}
		p.ErrorRate, p.AvgMs = ratio(p.Errors, p.Requests), ratio(ms, p.Requests)
		a := get(app)
		a.Platforms = append(a.Platforms, p)
		a.Requests += p.Requests
		a.Errors += p.Errors
		a.Slow += p.Slow
		totalMs[app] += ms
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	// People and versions per app.
	b = r.newQ()
	conds = b.dayBounds(f, "day", nil)
	if f.ClientApp != "" {
		conds = append(conds, "client_app = "+b.arg(f.ClientApp))
	}
	if f.User != "" {
		conds = append(conds, "CAST(user_id AS TEXT) = "+b.arg(f.User))
	}
	pw := where(conds)
	rows, err = r.db.QueryContext(ctx, `SELECT client_app, COUNT(DISTINCT user_id) FROM api_usage_people_daily`+pw+
		` GROUP BY client_app`, b.args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var app string
		var n int64
		if err := rows.Scan(&app, &n); err != nil {
			rows.Close()
			return nil, nil, err
		}
		get(app).People = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows, err = r.db.QueryContext(ctx, `SELECT client_app, client_version, COUNT(DISTINCT user_id)
		FROM api_usage_people_daily`+pw+` GROUP BY client_app, client_version`, b.args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var app string
		var v VersionUse
		if err := rows.Scan(&app, &v.Version, &v.People); err != nil {
			rows.Close()
			return nil, nil, err
		}
		a := get(app)
		a.Versions = append(a.Versions, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	out := make([]AppUsage, 0, len(by))
	for app, a := range by {
		a.ErrorRate, a.AvgMs = ratio(a.Errors, a.Requests), ratio(totalMs[app], a.Requests)
		sort.Slice(a.Platforms, func(i, j int) bool { return a.Platforms[i].Requests > a.Platforms[j].Requests })
		sort.Slice(a.Versions, func(i, j int) bool {
			if a.Versions[i].People != a.Versions[j].People {
				return a.Versions[i].People > a.Versions[j].People
			}
			return a.Versions[i].Version > a.Versions[j].Version
		})
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].ClientApp < out[j].ClientApp
	})

	trend, err := r.usageTrend(ctx, f)
	return out, trend, err
}

// usageTrend is the API's requests, errors and people per day.
func (r *repo) usageTrend(ctx context.Context, f Filters) ([]UsagePoint, error) {
	b := r.newQ()
	conds := b.dayBounds(f, "day", nil)
	if f.ClientApp != "" {
		conds = append(conds, "client_app = "+b.arg(f.ClientApp))
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+r.dayText("day")+`, COALESCE(SUM(requests),0), COALESCE(SUM(errors),0)
		FROM api_usage_daily`+where(conds)+` GROUP BY 1 ORDER BY 1`, b.args...)
	if err != nil {
		return nil, err
	}
	by := map[string]*UsagePoint{}
	for rows.Next() {
		p := &UsagePoint{}
		if err := rows.Scan(&p.Day, &p.Requests, &p.Errors); err != nil {
			rows.Close()
			return nil, err
		}
		by[p.Day] = p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if f.User != "" {
		conds = append(conds, "CAST(user_id AS TEXT) = "+b.arg(f.User))
	}
	rows, err = r.db.QueryContext(ctx, `SELECT `+r.dayText("day")+`, COUNT(DISTINCT user_id)
		FROM api_usage_people_daily`+where(conds)+` GROUP BY 1`, b.args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day string
		var n int64
		if err := rows.Scan(&day, &n); err != nil {
			rows.Close()
			return nil, err
		}
		p := by[day]
		if p == nil {
			p = &UsagePoint{Day: day}
			by[day] = p
		}
		p.People = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]UsagePoint, 0, len(by))
	for _, p := range by {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

func ratio(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// ─── Remote TV ─────────────────────────────────────────────────────────

// remoteDays reads the remote TV's delivered minutes and cost per day.
func (r *repo) remoteDays(ctx context.Context, f Filters) ([]RemoteDay, error) {
	b := r.newQ()
	conds := b.dayBounds(f, "day", nil)
	rows, err := r.db.QueryContext(ctx, `SELECT `+r.dayText("day")+`, minutes_delivered, simulcast_minutes, cost_usd
		FROM tv_remote_usage_daily`+where(conds)+` ORDER BY 1`, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RemoteDay{}
	for rows.Next() {
		var d RemoteDay
		if err := rows.Scan(&d.Day, &d.MinutesDelivered, &d.SimulcastMinutes, &d.CostUSD); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ─── Activity time series + heatmap ────────────────────────────────────

// bucketExpr returns the dialect SQL that truncates ev.started_at to the
// bucket, as a sortable text label.
func (r *repo) bucketExpr(bucket string) string {
	if r.isSQLite() {
		ts := r.ts("started_at")
		switch bucket {
		case "hour":
			return "strftime('%Y-%m-%dT%H:00', " + ts + ")"
		case "week":
			return "strftime('%Y-W%W', " + ts + ")"
		case "month":
			return "strftime('%Y-%m', " + ts + ")"
		default:
			return "strftime('%Y-%m-%d', " + ts + ")"
		}
	}
	switch bucket {
	case "hour":
		return "to_char(date_trunc('hour', started_at), 'YYYY-MM-DD\"T\"HH24:00')"
	case "week":
		return "to_char(date_trunc('week', started_at), 'IYYY-\"W\"IW')"
	case "month":
		return "to_char(date_trunc('month', started_at), 'YYYY-MM')"
	default:
		return "to_char(date_trunc('day', started_at), 'YYYY-MM-DD')"
	}
}

func (r *repo) series(ctx context.Context, f Filters, bucket string) ([]TimePoint, error) {
	b := r.newQ()
	q := b.with(f) + `
	SELECT b, COALESCE(SUM(CASE WHEN surface = 'video' THEN seconds ELSE 0 END),0),
	       COALESCE(SUM(CASE WHEN surface <> 'video' THEN seconds ELSE 0 END),0),
	       COALESCE(SUM(CASE WHEN surface = 'video' THEN 1 ELSE 0 END),0),
	       COALESCE(SUM(CASE WHEN surface <> 'video' THEN 1 ELSE 0 END),0)
	FROM (SELECT ` + r.bucketExpr(bucket) + ` AS b, surface, seconds FROM ev) x
	GROUP BY b ORDER BY b ASC`
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TimePoint{}
	for rows.Next() {
		var p TimePoint
		var label nullString
		if err := rows.Scan(&label, &p.VideoSec, &p.TVSec, &p.Plays, &p.Viewings); err != nil {
			return nil, err
		}
		p.Bucket = label.S
		p.WatchSec = p.VideoSec + p.TVSec
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *repo) heatCells(ctx context.Context, f Filters) ([]HeatCell, error) {
	var dowExpr, hourExpr string
	if r.isSQLite() {
		ts := r.ts("started_at")
		dowExpr = "CAST(strftime('%w', " + ts + ") AS INTEGER)"
		hourExpr = "CAST(strftime('%H', " + ts + ") AS INTEGER)"
	} else {
		dowExpr = "CAST(EXTRACT(DOW FROM started_at) AS INTEGER)"
		hourExpr = "CAST(EXTRACT(HOUR FROM started_at) AS INTEGER)"
	}
	b := r.newQ()
	q := b.with(f) + `
	SELECT ` + dowExpr + `, ` + hourExpr + `, COALESCE(SUM(seconds),0)
	FROM ev GROUP BY 1, 2`
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeatCell{}
	for rows.Next() {
		var c HeatCell
		if err := rows.Scan(&c.Dow, &c.Hour, &c.WatchSec); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
