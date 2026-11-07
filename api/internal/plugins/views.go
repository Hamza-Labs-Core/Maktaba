package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

// The Plugins page's reads (story 54.08 §3, 54.09 §4, 54.13).

// Names are a plugin's own words for its name and summary, in English and Arabic (from
// the package's i18n files; plugin text is data, shown escaped).
type Names struct {
	Name    map[string]string `json:"name"`
	Summary map[string]string `json:"summary"`
}

// DisplayName is a plugin's word for key in lang, from its package folder ("" when none).
func DisplayName(dir, key, lang string) string {
	b, err := os.ReadFile(filepath.Join(dir, "i18n", lang+".json"))
	if err != nil {
		return ""
	}
	var words map[string]any
	if json.Unmarshal(b, &words) != nil {
		return ""
	}
	s, _ := words[key].(string)
	return s
}

func namesOf(dir string, m manifestOf) Names {
	n := Names{Name: map[string]string{}, Summary: map[string]string{}}
	for _, lang := range []string{"en", "ar"} {
		n.Name[lang] = DisplayName(dir, m.Strings.Name, lang)
		n.Summary[lang] = DisplayName(dir, m.Strings.Summary, lang)
	}
	return n
}

// Health is a plugin's plugin_health row (54.09 §4).
type Health struct {
	Tripped       bool       `json:"tripped"`
	TrippedAt     *time.Time `json:"tripped_at,omitempty"`
	TripReason    string     `json:"trip_reason,omitempty"`
	JobsDone      int64      `json:"jobs_done"`
	JobsFailed    int64      `json:"jobs_failed"`
	LastErrorCode string     `json:"last_error_code,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
	// Recent are the last outcomes the dispatcher saw ({at, cause, ms, memory}).
	Recent json.RawMessage `json:"recent"`
}

// StageView is one of a plugin's stages and its queue.
type StageView struct {
	Name    string `json:"name"`
	Queue   string `json:"queue"`
	Subject string `json:"subject"`
	Version int    `json:"version"`
	Pending int    `json:"pending"`
	Running int    `json:"running"`
	Done24h int    `json:"done_24h"`
	Fail24h int    `json:"failed_24h"`
}

// View is one installed plugin as GET /api/plugins lists it.
type View struct {
	ID          string    `json:"id"`
	Component   string    `json:"component"`
	Version     string    `json:"version"`
	Digest      string    `json:"digest"`
	Names       Names     `json:"names"`
	ReviewLevel string    `json:"review_level"`
	Rating      int       `json:"rating"`
	InstalledAt time.Time `json:"installed_at"`
	// Want, Hold and Generation are its desired row; Phase and Reason its status.
	Want         string            `json:"want"`
	HeldBy       string            `json:"held_by,omitempty"`
	Generation   int64             `json:"generation"`
	Phase        string            `json:"phase"`
	Reason       string            `json:"reason,omitempty"`
	ReasonParams map[string]string `json:"reason_params,omitempty"`
	KeptOff      *time.Time        `json:"kept_off,omitempty"`
	Grants       GrantSet          `json:"grants"`
	Required     []string          `json:"required"`
	Optional     []string          `json:"optional"`
	Previous     string            `json:"previous_version,omitempty"`
	Health       Health            `json:"health"`
	// Revoked: a store `disable` covers it; Revocation the entry that applies (a `warn`
	// shows too, 54.17 §3). AutoUpdate is the admin's choice (54.17 §2).
	Revoked    bool            `json:"revoked"`
	Revocation json.RawMessage `json:"revocation,omitempty"`
	AutoUpdate bool            `json:"auto_update"`
}

// Detail is GET /api/plugins/{id}: the view, its manifest's summary, its stages and their
// queues, and its writes and data counts.
type Detail struct {
	View
	Manifest  json.RawMessage `json:"manifest"`
	Stages    []StageView     `json:"stages"`
	Writes    int64           `json:"writes"`
	DataRows  int64           `json:"data_rows"`
	DataBytes int64           `json:"data_bytes"`
	DataMB    int             `json:"data_mb"`
}

// List is every installed plugin, by id.
func (l *Lifecycle) List(ctx context.Context) ([]View, error) {
	rows, err := l.DB.QueryContext(ctx, `SELECT plugin_id FROM plugin_installs ORDER BY plugin_id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []View{}
	for _, id := range ids {
		v, err := l.view(ctx, id)
		if err != nil {
			return nil, err
		}
		if v != nil {
			out = append(out, *v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (l *Lifecycle) view(ctx context.Context, id string) (*View, error) {
	in, err := loadInstall(ctx, l.DB, id, false)
	if err != nil || in == nil {
		return nil, err
	}
	m, err := parseManifest(in.Manifest)
	if err != nil {
		return nil, err
	}
	v := &View{ID: id, Component: components.PluginPrefix + id, Version: in.Version, Digest: in.Digest,
		Names: namesOf(in.Path, m), ReviewLevel: in.ReviewLevel, Rating: pluginmanifest.Rating(m.asked(), in.ReviewLevel),
		InstalledAt: in.InstalledAt, Required: nonNil(m.Capabilities.Required), Optional: nonNil(m.Capabilities.Optional),
		Previous: in.PrevVersion.String, Grants: GrantSet{Capabilities: []string{}, Libraries: []string{}},
		Health: Health{Recent: json.RawMessage("[]")}}
	var rev []byte
	if err := l.DB.QueryRowContext(ctx, `SELECT revoked, revocation, auto_update FROM plugin_installs WHERE plugin_id = $1`, id).Scan(&v.Revoked, &rev, &v.AutoUpdate); err != nil {
		return nil, err
	}
	if len(rev) > 0 && string(rev) != "null" {
		v.Revocation = rev
	}
	if g, err := loadGrantSet(ctx, l.DB, id); err != nil {
		return nil, err
	} else if g != nil {
		v.Grants = *g
	}
	v.KeptOff = l.keptOff(ctx, id)
	if err := l.health(ctx, id, &v.Health); err != nil {
		return nil, err
	}
	if l.Components != nil {
		d, err := l.Components.Show(ctx, v.Component)
		var ref *svc.Refusal
		switch {
		case errors.As(err, &ref):
			// not in the live catalog (its manifest cannot be read here): no status
		case err != nil:
			return nil, err
		default:
			v.Want, v.Generation, v.Phase, v.Reason, v.ReasonParams = d.Want, d.Generation, d.Phase, d.Reason, d.ReasonParams
			if d.Hold != nil {
				v.HeldBy = d.Hold.By
			}
		}
	}
	return v, nil
}

func (l *Lifecycle) health(ctx context.Context, id string, h *Health) error {
	var at, errAt sql.NullTime
	var recent []byte
	err := l.DB.QueryRowContext(ctx, `SELECT tripped, tripped_at, trip_reason, jobs_done, jobs_failed, last_error_code,
		last_error_at, recent FROM plugin_health WHERE plugin_id = $1`, id).
		Scan(&h.Tripped, &at, &h.TripReason, &h.JobsDone, &h.JobsFailed, &h.LastErrorCode, &errAt, &recent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if at.Valid {
		h.TrippedAt = &at.Time
	}
	if errAt.Valid {
		h.LastErrorAt = &errAt.Time
	}
	h.Recent = recent
	return nil
}

// Show is one plugin's detail (404 plugin_not_installed).
func (l *Lifecycle) Show(ctx context.Context, id string) (*Detail, error) {
	v, err := l.view(ctx, id)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, problem(http.StatusNotFound, CodeNotInstalled, "%s is not installed", id)
	}
	in, err := loadInstall(ctx, l.DB, id, false)
	if err != nil || in == nil {
		return nil, err
	}
	m, _ := parseManifest(in.Manifest)
	d := &Detail{View: *v, Manifest: in.Manifest, Stages: []StageView{}, DataMB: m.Resources.DataMB}
	for _, s := range m.Stage {
		sv := StageView{Name: s.Name, Queue: components.PluginQueueName(id, s.Name), Subject: s.Subject, Version: s.Version}
		_ = l.DB.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE state = 'pending'),
			count(*) FILTER (WHERE state IN ('claimed', 'running')),
			count(*) FILTER (WHERE state = 'done' AND finished_at > now() - interval '1 day'),
			count(*) FILTER (WHERE state = 'failed' AND finished_at > now() - interval '1 day')
			FROM processing_jobs WHERE stage = $1`, sv.Queue).Scan(&sv.Pending, &sv.Running, &sv.Done24h, &sv.Fail24h)
		d.Stages = append(d.Stages, sv)
	}
	_ = l.DB.QueryRowContext(ctx, `SELECT count(*) FROM plugin_writes WHERE plugin_id = $1 AND reverted_at IS NULL`, id).Scan(&d.Writes)
	_ = l.DB.QueryRowContext(ctx, `SELECT count(*), COALESCE(sum(bytes), 0) FROM plugin_data WHERE plugin_id = $1`, id).
		Scan(&d.DataRows, &d.DataBytes)
	return d, nil
}

// Names are a plugin entry's own name and summary by language (the components service's
// PluginHooks): read from its package's words.
func (l *Lifecycle) Names(cid string) map[string]map[string]string {
	id := strings.TrimPrefix(cid, components.PluginPrefix)
	in, err := loadInstall(context.Background(), l.DB, id, false)
	if err != nil || in == nil {
		return nil
	}
	m, err := parseManifest(in.Manifest)
	if err != nil {
		return nil
	}
	n := namesOf(in.Path, m)
	return map[string]map[string]string{"name": n.Name, "summary": n.Summary}
}
