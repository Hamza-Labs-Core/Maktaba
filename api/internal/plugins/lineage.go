package plugins

// Run-time lineage (README §4.12, story 54.40 §3): the second line against loops. Every
// plugin job carries where its chain started, how many plugin jobs it has passed through
// and which (plugin stage, subject) pairs. A request or an emitted event that would pass
// the hop cap, or re-enter a pair already in the path, is refused as plugin_loop; so is a
// job a core event started on a subject a plugin wrote less than DataWindow ago, when the
// writer's lineage already holds the job's own pair (a loop through data).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const (
	// CodeLoop is a request, an event or a job that would loop.
	CodeLoop = "plugin_loop"
	// MaxHops is the lineage hop cap (README §4.7).
	MaxHops = 16
	// DataWindow is how long a plugin write's lineage stays on its subject.
	DataWindow = 10 * time.Minute
	// LineageKey is the reserved key of a job's payload that holds its lineage; the
	// pipeline strips it before the guest reads the payload, and a plugin may not send it.
	LineageKey = "_lineage"
)

// Origin is where a chain started.
type Origin struct {
	// Kind is core (a core edge enqueued the job), schedule, admin, or plugin (a request
	// or an event of another job: Lineage.Path holds the chain then)
	Kind    string `json:"kind"`
	Stage   string `json:"stage,omitempty"`
	Subject string `json:"subject,omitempty"`
}

// Lineage is a plugin job's lineage. Path pairs are [queue stage name, "kind:id"].
type Lineage struct {
	Origin Origin      `json:"origin"`
	Hops   int         `json:"hops"`
	Path   [][2]string `json:"path"`
}

func subjectKey(kind, id string) string { return kind + ":" + id }

// Holds reports whether the path already has the pair.
func (l Lineage) Holds(stage, subject string) bool {
	for _, p := range l.Path {
		if p[0] == stage && p[1] == subject {
			return true
		}
	}
	return false
}

// Next is the lineage of a job this one starts on (stage, subject), or false when that
// would loop: past the hop cap, or a pair already in the path.
func (l Lineage) Next(stage, subject string) (Lineage, bool) {
	if l.Hops+1 > MaxHops || l.Holds(stage, subject) {
		return Lineage{}, false
	}
	path := append(append([][2]string(nil), l.Path...), [2]string{stage, subject})
	return Lineage{Origin: l.Origin, Hops: l.Hops + 1, Path: path}, true
}

// lineageOf reads a job's lineage at mint: the one its payload carries (a request or an
// event of another plugin job), else a new one from its origin, inheriting a plugin
// write's lineage on its subject from the last DataWindow. It answers plugin_loop when the
// job itself closes a loop.
func lineageOf(ctx context.Context, db *sql.DB, payload []byte, stage, kind, subject string, now time.Time) (Lineage, *Refusal) {
	key := subjectKey(kind, subject)
	var p map[string]json.RawMessage
	if len(payload) > 0 && json.Unmarshal(payload, &p) == nil {
		if raw, ok := p[LineageKey]; ok {
			var l Lineage
			if json.Unmarshal(raw, &l) == nil && l.Hops > 0 {
				if l.Hops > MaxHops {
					return l, refuse(409, CodeLoop, "past the hop cap")
				}
				return l, nil
			}
		}
	}
	start := Lineage{Origin: Origin{Kind: "core", Stage: stage, Subject: key}}
	var raw []byte
	var at time.Time
	err := db.QueryRowContext(ctx, `SELECT lineage, written_at FROM plugin_write_lineage
		WHERE subject_kind = $1 AND subject_id = $2`, kind, subject).Scan(&raw, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return start, refuse(503, CodeMediaGone, err.Error())
	case now.Sub(at) <= DataWindow:
		var w Lineage
		if json.Unmarshal(raw, &w) == nil && w.Hops > 0 {
			w.Origin = Origin{Kind: "plugin", Stage: w.Origin.Stage, Subject: w.Origin.Subject}
			next, ok := w.Next(stage, key)
			if !ok {
				return w, refuse(409, CodeLoop, "a loop through data: "+stage+" on "+key)
			}
			return next, nil
		}
	}
	next, _ := start.Next(stage, key)
	return next, nil
}

// stampWrite leaves the invocation's lineage on the subject it wrote (in the write's
// transaction, so a refused write leaves nothing).
func (c *call) stampWrite(ctx context.Context, tx *sql.Tx, kind, subject string) error {
	l, _ := json.Marshal(c.inv.Lineage)
	_, err := tx.ExecContext(ctx, `INSERT INTO plugin_write_lineage (subject_kind, subject_id, plugin_id, stage, lineage, written_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (subject_kind, subject_id) DO UPDATE SET plugin_id = EXCLUDED.plugin_id, stage = EXCLUDED.stage,
			lineage = EXCLUDED.lineage, written_at = EXCLUDED.written_at`, kind, subject, c.inv.PluginID, c.inv.Stage, l)
	return err
}
