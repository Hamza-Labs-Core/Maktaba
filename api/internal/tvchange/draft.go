package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Producer kinds of a draft (tv_changes.source.kind): one open draft per kind (design
// §11.2). The pipeline writes write_channels and schedule drafts; the API the others.
const (
	SourceEditor        = "editor"
	SourceSettings      = "settings"
	SourceManual        = "manual"
	SourceWriteChannels = "write_channels"
	SourceSchedule      = "schedule"
)

// Pending is a change's diff.pending: what its producer wants (the shape the
// pipeline's tvrelease/build.py reads):
//
//	channels  {channel id: def | null}    def = the full channels row; null = removed
//	art       {channel id: {kind: override | null}}
//	sched     [channel id]                forced regenerations
//	settings  {"tv" | "tv_output": stored overrides}
//	edited    [channel id]                marked edited by a person at the swap
type Pending struct {
	M map[string]any
}

func (p *Pending) ensure() {
	if p.M == nil {
		p.M = map[string]any{}
	}
}

func (p *Pending) sub(key string) map[string]any {
	p.ensure()
	m, _ := p.M[key].(map[string]any)
	if m == nil {
		m = map[string]any{}
		p.M[key] = m
	}
	return m
}

// Channels is the pending definitions by channel id (a nil value: removed).
func (p *Pending) Channels() map[string]any {
	if p.M == nil {
		return map[string]any{}
	}
	m, _ := p.M["channels"].(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Channel is the pending definition of id: (def, removed, present).
func (p *Pending) Channel(id string) (map[string]any, bool, bool) {
	v, ok := p.Channels()[id]
	if !ok {
		return nil, false, false
	}
	if v == nil {
		return nil, true, true
	}
	d, _ := v.(map[string]any)
	return d, false, true
}

// SetChannel records id's definition as it will be.
func (p *Pending) SetChannel(id string, def map[string]any) { p.sub("channels")[id] = def }

// RemoveChannel records id's removal.
func (p *Pending) RemoveChannel(id string) { p.sub("channels")[id] = nil }

// DropChannel forgets id altogether (a channel added in this draft, then deleted).
func (p *Pending) DropChannel(id string) {
	delete(p.sub("channels"), id)
	delete(p.sub("art"), id)
}

// Art is id's pending art by kind (a nil value: back to the plan's).
func (p *Pending) Art(id string) map[string]any {
	if p.M == nil {
		return nil
	}
	a, _ := p.M["art"].(map[string]any)
	m, _ := a[id].(map[string]any)
	return m
}

// SetArt records kind's hand-set art of id (nil: reverted).
func (p *Pending) SetArt(id, kind string, override map[string]any) {
	a := p.sub("art")
	m, _ := a[id].(map[string]any)
	if m == nil {
		m = map[string]any{}
		a[id] = m
	}
	if override == nil {
		m[kind] = nil
	} else {
		m[kind] = override
	}
}

func (p *Pending) list(key string) []string {
	p.ensure()
	out := []string{}
	switch xs := p.M[key].(type) {
	case []any:
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, xs...)
	}
	return out
}

func (p *Pending) add(key string, ids ...string) {
	set := map[string]bool{}
	for _, s := range p.list(key) {
		set[s] = true
	}
	for _, s := range ids {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	p.M[key] = out
}

// AddSched marks channels for a forced regeneration (a schedule change).
func (p *Pending) AddSched(ids ...string) { p.add("sched", ids...) }

// Sched is the forced regenerations.
func (p *Pending) Sched() []string { return p.list("sched") }

// AddEdited marks channels as edited by a person (applied at the swap).
func (p *Pending) AddEdited(ids ...string) { p.add("edited", ids...) }

// SetSettings records a rules section as it will be (its stored overrides).
func (p *Pending) SetSettings(section string, overrides map[string]json.RawMessage) {
	s := p.sub("settings")
	m := map[string]any{}
	for k, raw := range overrides {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			m[k] = v
		}
	}
	s[section] = m
}

// Settings is the pending sections.
func (p *Pending) Settings() map[string]any {
	if p.M == nil {
		return map[string]any{}
	}
	m, _ := p.M["settings"].(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

// DraftRef names the draft a save went to.
type DraftRef struct {
	ID     string `json:"id"`
	Number int64  `json:"number"`
	State  string `json:"state"`
}

// Drafts is the open drafts the channel editor and the TV settings save into while
// staging is on (story 33.12 AC6, AC8). Nil-safe: a nil *Drafts is staging off.
type Drafts struct {
	DB    *sql.DB
	Store *Store
	// Enabled overrides the rules.tv_staging.enabled read (tests); nil reads it.
	EnabledFunc func(ctx context.Context) bool
}

// NewDrafts wires drafts over db.
func NewDrafts(db *sql.DB) *Drafts { return &Drafts{DB: db, Store: &Store{DB: db}} }

// Enabled reports whether saves go to drafts (rules.tv_staging.enabled and the
// staging tables exist).
func (d *Drafts) Enabled(ctx context.Context) bool {
	if d == nil || d.DB == nil {
		return false
	}
	if d.EnabledFunc != nil {
		return d.EnabledFunc(ctx)
	}
	c, err := LoadConfig(ctx, d.DB)
	if err != nil || !c.Enabled {
		return false
	}
	var n int
	if err := d.DB.QueryRowContext(ctx, `SELECT count(*) FROM tv_changes WHERE id IS NULL`).Scan(&n); err != nil {
		return false // a database before slot 0127
	}
	return true
}

// Open is the open draft of kind (nil when none).
func (d *Drafts) Open(ctx context.Context, kind string) (*Change, error) {
	return d.Store.OpenDraft(ctx, d.DB, kind)
}

func lockKey() string { return "tv_change_draft" }

// Save applies fn to the open draft of kind (created with title when there is none),
// in one transaction, and records the event and the audit row.
func (d *Drafts) Save(ctx context.Context, p *principal.Principal, kind, title, what string,
	fn func(pd *Pending) error) (DraftRef, error) {
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return DraftRef{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if !IsSQLite(d.DB) {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey()); err != nil {
			return DraftRef{}, err
		}
	}
	c, err := d.Store.OpenDraft(ctx, tx, kind)
	if err != nil {
		return DraftRef{}, err
	}
	var by *string
	if p != nil && p.UserID != "" {
		u := p.UserID
		by = &u
	}
	created := false
	if c == nil {
		c = &Change{State: StateDraft, Title: title, Source: map[string]any{"kind": kind},
			Diff: map[string]any{"pending": map[string]any{}}, CreatedBy: by}
		if err := d.Store.Insert(ctx, tx, c); err != nil {
			return DraftRef{}, err
		}
		created = true
	}
	pd := c.Pending()
	if err := fn(pd); err != nil {
		return DraftRef{}, err
	}
	pd.ensure()
	diff := c.Diff
	if diff == nil {
		diff = map[string]any{}
	}
	diff["pending"] = pd.M
	diff["summary"] = Summarize(pd)
	fields := map[string]any{"diff": diff}
	withdrawn := !created && len(c.ApprovedBy) > 0
	if withdrawn {
		// an approval given ahead covered what the draft held then, not this edit
		fields["approved_by"] = []string{}
		fields["approved_at"] = (*time.Time)(nil)
	}
	if err := d.Store.Set(ctx, tx, c.ID, fields); err != nil {
		return DraftRef{}, err
	}
	if withdrawn {
		st := StateDraft
		if err := d.Store.Event(ctx, tx, c.ID, Event{ActorUserID: by, ActorSource: actorSource(by, kind),
			FromState: &st, ToState: &st, Action: "approval_withdrawn",
			Detail: map[string]any{"approved_by": c.ApprovedBy, "what": what}}); err != nil {
			return DraftRef{}, err
		}
	}
	if !created {
		st := StateDraft
		if err := d.Store.Event(ctx, tx, c.ID, Event{ActorUserID: by, ActorSource: actorSource(by, kind),
			FromState: &st, ToState: &st, Action: "append", Detail: map[string]any{"what": what}}); err != nil {
			return DraftRef{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return DraftRef{}, err
	}
	uid := ""
	if by != nil {
		uid = *by
	}
	// after the commit: a failed audit insert must not abort the save's transaction
	d.Store.Audit(ctx, d.DB, uid, "draft_"+what, c.ID, map[string]any{"kind": kind})
	var num int64
	_ = d.DB.QueryRowContext(ctx, `SELECT number FROM tv_changes WHERE id = $1`, c.ID).Scan(&num)
	return DraftRef{ID: c.ID, Number: num, State: StateDraft}, nil
}

// Summarize counts a draft's pending edits (shown before it is staged).
func Summarize(pd *Pending) map[string]any {
	removed, changed := 0, 0
	for _, v := range pd.Channels() {
		if v == nil {
			removed++
			continue
		}
		changed++
	}
	return map[string]any{
		"channels_changed": changed,
		"channels_removed": removed,
		"art":              len(mapOf(pd.M["art"])),
		"sched":            len(pd.Sched()),
		"settings":         sortedKeys(pd.Settings()),
		"at":               time.Now().UTC().Format(time.RFC3339),
	}
}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SettingsSections are the rules sections a TV release snapshots (plan 33.2 D4).
var SettingsSections = map[string]bool{"tv": true, "tv_output": true}

// StagesSection implements rules.SettingsStager.
func (d *Drafts) StagesSection(ctx context.Context, section string) bool {
	return SettingsSections[section] && d.Enabled(ctx)
}

// SaveSettingsDraft implements rules.SettingsStager: the section's overrides as they
// will be go into the open settings draft.
func (d *Drafts) SaveSettingsDraft(ctx context.Context, p *principal.Principal, section string,
	overrides map[string]json.RawMessage) (string, int64, error) {
	ref, err := d.Save(ctx, p, SourceSettings, "TV settings", "settings_"+section, func(pd *Pending) error {
		pd.SetSettings(section, overrides)
		return nil
	})
	return ref.ID, ref.Number, err
}

// PendingSection implements rules.SettingsStager.
func (d *Drafts) PendingSection(ctx context.Context, section string) (map[string]json.RawMessage, bool) {
	c, err := d.Open(ctx, SourceSettings)
	if err != nil || c == nil {
		return nil, false
	}
	m, ok := c.Pending().Settings()[section].(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		b, err := json.Marshal(v)
		if err == nil {
			out[k] = b
		}
	}
	return out, true
}
