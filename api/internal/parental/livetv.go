package parental

// Live TV under parental controls (Epic 57, story 57.10): which channels a restricted
// account sees, which programmes on them are masked, and whether what a channel is
// playing now may be watched.
//
// Each check is one small function over an Effective, so the surface registry of
// story 57.03 can name them:
//
//	ChannelPermitted    one channel
//	ProgrammeTier       the tier a programme is judged at
//	ProgrammePermitted  one programme on a permitted channel
//
// and LiveTV loads what they need from the database. Every load error is an error to
// the caller, who refuses: nothing here answers "allowed" on a failure.

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// ChannelFacts is what the rules need to know about a channel.
type ChannelFacts struct {
	ID string
	// Tier is the tier an admin set on the channel (nil: unrated).
	Tier *int
	// LibraryID is the channel's library (nil: a channel over several libraries).
	LibraryID *string
}

// ChannelItem is the channel as the rules see it: its tier, itself and its library.
func ChannelItem(c ChannelFacts) Item {
	it := Item{Tier: c.Tier, Subjects: []Subject{{Kind: SubjectChannel, ID: strings.ToLower(c.ID)}}}
	if c.LibraryID != nil && *c.LibraryID != "" {
		it.Subjects = append(it.Subjects, Subject{Kind: SubjectLibrary, ID: strings.ToLower(*c.LibraryID)})
	}
	return it
}

// ChannelPermitted reports whether e's account may see and tune the channel. A
// channel with no tier is unrated: the account's unrated rule or an allow decides.
func ChannelPermitted(e *Effective, c ChannelFacts, now time.Time) bool {
	return e.Permits(ChannelItem(c), now)
}

// ProgrammeFacts is what the rules need to know about one scheduled programme.
type ProgrammeFacts struct {
	ChannelID string
	Seq       int64
	StartAt   time.Time
	EndAt     time.Time
	// Rating is the guide's rating string for the programme ("" when none).
	Rating string
	// StoredTier is channel_programs.age_tier, written when the guide was planned.
	StoredTier *int
	// VideoID is the scheduled library video ("" for filler and idents).
	VideoID string
	// VideoTier is that video's tier now (nil: unrated or no video).
	VideoTier *int
	// VideoLibraryID, SeriesIDs and CollectionIDs are what the video sits in.
	VideoLibraryID string
	SeriesIDs      []string
	CollectionIDs  []string
}

// ProgrammeTier is the tier a programme is judged at: the guide's rating read through
// the tables, else the scheduled video's tier now, else the tier stored when the guide
// was written, else the channel's tier. nil: unrated.
//
// The video's tier is read now, ahead of the stored one, so a video rated again after
// the guide was planned is judged at its new tier.
func ProgrammeTier(t *Table, rating string, videoTier, storedTier, channelTier *int) *int {
	if rating = strings.TrimSpace(rating); rating != "" {
		if t == nil {
			t = Default()
		}
		if tier, ok := t.Content(rating); ok {
			return &tier
		}
	}
	if videoTier != nil {
		return videoTier
	}
	if storedTier != nil {
		return storedTier
	}
	return channelTier
}

// ProgrammeItem is the programme as the rules see it: its tier and everything it is
// or sits in (the video, its library, series and collections, the channel and the
// channel's library).
func ProgrammeItem(t *Table, c ChannelFacts, p ProgrammeFacts) Item {
	it := Item{Tier: ProgrammeTier(t, p.Rating, p.VideoTier, p.StoredTier, c.Tier)}
	if p.VideoID != "" {
		it.Subjects = append(it.Subjects, Subject{Kind: SubjectVideo, ID: strings.ToLower(p.VideoID)})
	}
	if p.VideoLibraryID != "" {
		it.Subjects = append(it.Subjects, Subject{Kind: SubjectLibrary, ID: strings.ToLower(p.VideoLibraryID)})
	}
	for _, id := range p.SeriesIDs {
		it.Subjects = append(it.Subjects, Subject{Kind: SubjectSeries, ID: strings.ToLower(id)})
	}
	for _, id := range p.CollectionIDs {
		it.Subjects = append(it.Subjects, Subject{Kind: SubjectCollection, ID: strings.ToLower(id)})
	}
	it.Subjects = append(it.Subjects, ChannelItem(c).Subjects...)
	return it
}

// ProgrammePermitted reports whether e's account may see the programme's details and
// watch it. It is asked only about a channel the account is permitted.
func ProgrammePermitted(e *Effective, t *Table, c ChannelFacts, p ProgrammeFacts, now time.Time) bool {
	return e.Permits(ProgrammeItem(t, c, p), now)
}

// LiveTV answers the live TV checks from the database.
type LiveTV struct {
	DB *sql.DB
	// Cache gives an account's effective policy.
	Cache *Cache
	// Table reads rating strings (nil: the built-in tables).
	Table *Table
	// Now is the clock (nil: time.Now).
	Now func() time.Time
}

// NewLiveTV returns the live TV checks over db. cache nil: one that reads every
// policy from the database.
func NewLiveTV(db *sql.DB, cache *Cache) *LiveTV {
	if cache == nil {
		cache = NewCache(NewStore(db))
	}
	return &LiveTV{DB: db, Cache: cache}
}

func (l *LiveTV) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// LiveView is the live TV filter of one account for one request.
type LiveView struct {
	l   *LiveTV
	Eff *Effective
	Now time.Time
}

// View is the filter of the request's principal.
func (l *LiveTV) View(ctx context.Context, p *principal.Principal) (*LiveView, error) {
	e, err := l.Cache.For(ctx, p)
	if err != nil {
		return nil, err
	}
	return &LiveView{l: l, Eff: e, Now: l.now()}, nil
}

// ViewOf is the filter of the account userID (the streaming service asks by id).
func (l *LiveTV) ViewOf(ctx context.Context, userID string) (*LiveView, error) {
	if !isUUID(userID) {
		return nil, errors.New("parental: not a user id")
	}
	e, err := l.Cache.Effective(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &LiveView{l: l, Eff: e, Now: l.now()}, nil
}

// Restricted reports whether the account is limited at all. When it is not, every
// check passes and nothing is read.
func (v *LiveView) Restricted() bool { return v != nil && v.Eff != nil && v.Eff.Restricted }

// ChannelFacts loads the facts of the channels ids (unknown ids are left out).
func (l *LiveTV) ChannelFacts(ctx context.Context, ids []string) (map[string]ChannelFacts, error) {
	out := map[string]ChannelFacts{}
	uuids := make([]string, 0, len(ids))
	for _, id := range ids {
		if isUUID(id) {
			uuids = append(uuids, id)
		}
	}
	if len(uuids) == 0 {
		return out, nil
	}
	rows, err := l.DB.QueryContext(ctx,
		`SELECT id::text, age_tier, library_id::text FROM channels WHERE id = ANY($1::uuid[])`, pq.Array(uuids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ChannelFacts
		var tier sql.NullInt64
		var lib sql.NullString
		if err := rows.Scan(&c.ID, &tier, &lib); err != nil {
			return nil, err
		}
		c.Tier = intPtr(tier)
		if lib.Valid {
			c.LibraryID = &lib.String
		}
		out[strings.ToLower(c.ID)] = c
	}
	return out, rows.Err()
}

// Channels answers which of the channels ids the account is permitted, with their
// facts. An unrestricted account is permitted every id asked about, with no read.
func (v *LiveView) Channels(ctx context.Context, ids []string) (map[string]ChannelFacts, error) {
	if !v.Restricted() {
		out := make(map[string]ChannelFacts, len(ids))
		for _, id := range ids {
			out[strings.ToLower(id)] = ChannelFacts{ID: id}
		}
		return out, nil
	}
	facts, err := v.l.ChannelFacts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, c := range facts {
		if !ChannelPermitted(v.Eff, c, v.Now) {
			delete(facts, id)
		}
	}
	return facts, nil
}

// Channel reports whether the account is permitted the channel id.
func (v *LiveView) Channel(ctx context.Context, id string) (ChannelFacts, bool, error) {
	m, err := v.Channels(ctx, []string{id})
	if err != nil {
		return ChannelFacts{}, false, err
	}
	c, ok := m[strings.ToLower(id)]
	return c, ok, nil
}

// Programmes loads the live schedule of the channels in chans over [start, end),
// each channel's programmes in time order.
func (l *LiveTV) Programmes(ctx context.Context, chans map[string]ChannelFacts, start, end time.Time) (map[string][]ProgrammeFacts, error) {
	out := map[string][]ProgrammeFacts{}
	ids := make([]string, 0, len(chans))
	for _, c := range chans {
		if isUUID(c.ID) {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := l.DB.QueryContext(ctx, `
		SELECT cp.channel_id::text, cp.seq, cp.start_at, cp.end_at,
		       COALESCE(cp.title_snapshot->>'rating', ''), cp.age_tier,
		       COALESCE(v.id::text, ''), v.age_tier, COALESCE(v.library_id::text, ''),
		       ARRAY(SELECT se.series_id::text FROM series_episodes se WHERE se.video_id = v.id),
		       ARRAY(SELECT ci.collection_id::text FROM collection_items ci WHERE ci.video_id = v.id)
		FROM channel_programs cp
		LEFT JOIN videos v ON v.id = cp.video_id
		WHERE cp.sched_gen = tv_live_gen() AND cp.channel_id = ANY($1::uuid[])
		  AND cp.start_at < $3 AND cp.end_at > $2
		ORDER BY cp.channel_id, cp.start_at`, pq.Array(ids), start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p ProgrammeFacts
		var stored, vtier sql.NullInt64
		if err := rows.Scan(&p.ChannelID, &p.Seq, &p.StartAt, &p.EndAt, &p.Rating, &stored,
			&p.VideoID, &vtier, &p.VideoLibraryID, pq.Array(&p.SeriesIDs), pq.Array(&p.CollectionIDs)); err != nil {
			return nil, err
		}
		p.StoredTier, p.VideoTier = intPtr(stored), intPtr(vtier)
		key := strings.ToLower(p.ChannelID)
		out[key] = append(out[key], p)
	}
	return out, rows.Err()
}

// ProgrammeVerdicts answers, for the programmes of one read, which are permitted.
type ProgrammeVerdicts struct {
	// all: the account is unrestricted.
	all   bool
	bySeq map[string]map[int64]bool
	spans map[string][]verdictSpan
}

type verdictSpan struct {
	start, end time.Time
	allowed    bool
}

// Allowed reports whether the programme seq of the channel is permitted. A programme
// the read did not see is not.
func (pv *ProgrammeVerdicts) Allowed(channelID string, seq int64) bool {
	if pv == nil {
		return false
	}
	if pv.all {
		return true
	}
	return pv.bySeq[strings.ToLower(channelID)][seq]
}

// AllowedAt reports whether what the channel plays at t is permitted. Between
// programmes the channel plays its own filler, which is.
func (pv *ProgrammeVerdicts) AllowedAt(channelID string, t time.Time) bool {
	if pv == nil {
		return false
	}
	if pv.all {
		return true
	}
	for _, s := range pv.spans[strings.ToLower(channelID)] {
		if !t.Before(s.start) && t.Before(s.end) {
			return s.allowed
		}
	}
	return true
}

// Programmes judges the programmes of the permitted channels chans (as Channels
// answered them) over [start, end).
func (v *LiveView) Programmes(ctx context.Context, chans map[string]ChannelFacts, start, end time.Time) (*ProgrammeVerdicts, error) {
	if !v.Restricted() {
		return &ProgrammeVerdicts{all: true}, nil
	}
	progs, err := v.l.Programmes(ctx, chans, start, end)
	if err != nil {
		return nil, err
	}
	pv := &ProgrammeVerdicts{bySeq: map[string]map[int64]bool{}, spans: map[string][]verdictSpan{}}
	for id, list := range progs {
		c, ok := chans[id]
		if !ok {
			continue
		}
		seqs := make(map[int64]bool, len(list))
		for _, p := range list {
			ok := ProgrammePermitted(v.Eff, v.l.Table, c, p, v.Now)
			seqs[p.Seq] = ok
			pv.spans[id] = append(pv.spans[id], verdictSpan{start: p.StartAt, end: p.EndAt, allowed: ok})
		}
		pv.bySeq[id] = seqs
	}
	return pv, nil
}

// LiveVerdict is whether an account may watch a channel now.
type LiveVerdict struct {
	// Restricted: the account has a policy. When false, the rest says "allowed".
	Restricted bool
	// Channel: the account is permitted the channel at all.
	Channel bool
	// Allowed: the channel is permitted and so is what it plays now.
	Allowed bool
	// Until is when Allowed next changes (the start of the first later programme
	// judged the other way); nil when no change is in the schedule read.
	Until *time.Time
}

// verdictHorizon is how far ahead Verdict reads the schedule for Until.
const verdictHorizon = 12 * time.Hour

// Verdict answers whether the account may watch the channel now, and until when.
func (v *LiveView) Verdict(ctx context.Context, channelID string) (LiveVerdict, error) {
	if !v.Restricted() {
		return LiveVerdict{Channel: true, Allowed: true}, nil
	}
	out := LiveVerdict{Restricted: true}
	c, ok, err := v.Channel(ctx, channelID)
	if err != nil {
		return LiveVerdict{}, err
	}
	if !ok {
		return out, nil
	}
	out.Channel = true
	chans := map[string]ChannelFacts{strings.ToLower(c.ID): c}
	pv, err := v.Programmes(ctx, chans, v.Now, v.Now.Add(verdictHorizon))
	if err != nil {
		return LiveVerdict{}, err
	}
	out.Allowed = pv.AllowedAt(c.ID, v.Now)
	spans := pv.spans[strings.ToLower(c.ID)]
	sort.Slice(spans, func(i, j int) bool { return spans[i].start.Before(spans[j].start) })
	for _, s := range spans {
		if s.start.After(v.Now) && s.allowed != out.Allowed {
			at := s.start
			out.Until = &at
			break
		}
	}
	// a blocked programme followed by a gap: the channel's own filler is allowed
	if !out.Allowed && out.Until == nil {
		for _, s := range spans {
			if !v.Now.Before(s.start) && v.Now.Before(s.end) {
				at := s.end
				out.Until = &at
			}
		}
	}
	return out, nil
}
