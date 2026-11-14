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
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
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
	// Problems are its failed jobs open now; AvgMS and P95MS the last day's done jobs' time.
	Problems int    `json:"problems"`
	AvgMS    *int64 `json:"avg_ms,omitempty"`
	P95MS    *int64 `json:"p95_ms,omitempty"`
	// RunsIn is where it runs now: a runner group (light, files, net, service, ml, index),
	// "worker" (the pipeline worker) or "host" (the plugin host).
	RunsIn string `json:"runs_in,omitempty"`
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
	// Story 54.44, the same for every plugin: who publishes it, where this home got it
	// (release, store, file), its stages, why its switch cannot be used ("" when it can),
	// its jobs and what it uses. Settings are the settings its component owns
	// (rules.<section>, toml:<key>), for a plugin whose settings are Maktaba's own pages.
	Publisher  Publisher `json:"publisher"`
	Source     string    `json:"source"`
	StageNames []string  `json:"stage_names"`
	// Queues are its stages as the queue names them (the failures view's stage filter): a
	// Maktaba plugin's are its stage names, any other's p_<hash>_<stage>.
	Queues       []string  `json:"queues"`
	SwitchLocked string    `json:"switch_locked,omitempty"`
	Activity     Activity  `json:"activity"`
	Resources    Resources `json:"resources"`
	Settings     []string  `json:"settings"`
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
	// ReportLogs: an admin ticked "Include its logs in problem reports" (54.09 §3).
	ReportLogs bool `json:"report_logs"`
	// Capabilities are what a Maktaba-published plugin can do, in Maktaba's words (a
	// third-party plugin's are Required, Optional and Grants). Machines is its status on
	// each machine.
	Capabilities []TrustedCapability `json:"capabilities"`
	Machines     []MachineState      `json:"machines"`
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
		Health: Health{Recent: json.RawMessage("[]")}, Source: SourceFile, StageNames: []string{}, Settings: []string{},
		Resources: Resources{Processes: []ProcessUse{}}}
	if in.ReviewLevel == "store" || in.ReviewLevel == "automated" || in.ReviewLevel == "reviewed" {
		v.Source = SourceStore
	}
	v.Queues = []string{}
	for _, s := range m.Stage {
		v.StageNames = append(v.StageNames, s.Name)
		v.Queues = append(v.Queues, components.PluginQueueName(id, s.Name))
	}
	v.Resources.DataMB, v.Resources.MemoryMB = m.Resources.DataMB, m.Resources.MemoryMB
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
	v.SwitchLocked = lockOf(v.Reason, v.HeldBy)
	if v.Revoked {
		v.SwitchLocked = "revoked"
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

// Show is one plugin's detail (404 plugin_not_installed): an installed plugin's, or a
// Maktaba-published plugin's (story 54.44).
func (l *Lifecycle) Show(ctx context.Context, id string) (*Detail, error) {
	if fp := firstparty.ByID(id); fp != nil && IsPublished(id) {
		return l.showPublished(ctx, fp)
	}
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
	d := &Detail{View: *v, Manifest: in.Manifest, Stages: []StageView{}, DataMB: m.Resources.DataMB,
		Capabilities: []TrustedCapability{}, Machines: []MachineState{}}
	var queues, enabled []string
	for _, s := range m.Stage {
		queues = append(queues, components.PluginQueueName(id, s.Name))
	}
	page, err := l.loadPage(ctx, queues)
	if err != nil {
		return nil, err
	}
	for i, s := range m.Stage {
		a := page.activity[queues[i]]
		d.Stages = append(d.Stages, StageView{Name: s.Name, Queue: queues[i], Subject: s.Subject, Version: s.Version,
			Pending: a.Pending, Running: a.Running, Done24h: a.Done24h, Fail24h: a.Failed24h, Problems: a.Problems,
			AvgMS: a.AvgMS, P95MS: a.P95MS, RunsIn: "host"})
	}
	_ = l.DB.QueryRowContext(ctx, `SELECT count(*) FROM plugin_writes WHERE plugin_id = $1 AND reverted_at IS NULL`, id).Scan(&d.Writes)
	if set, err := ReportLogPlugins(ctx, l.DB); err == nil {
		d.ReportLogs = set[id]
	}
	_ = l.DB.QueryRowContext(ctx, `SELECT count(*), COALESCE(sum(bytes), 0) FROM plugin_data WHERE plugin_id = $1`, id).
		Scan(&d.DataRows, &d.DataBytes)
	// the other plugins the host runs: every enabled one
	_ = l.each(ctx, `SELECT trim(leading $1 from id) FROM components WHERE id LIKE $1 || '%' AND want = 'enabled'`,
		components.PluginPrefix, func(scan func(...any) error) error {
			var other string
			if err := scan(&other); err != nil {
				return err
			}
			enabled = append(enabled, other)
			return nil
		})
	d.Activity = page.activityOf(queues)
	d.Resources = l.thirdPartyResources(&d.View, page, enabled)
	d.Resources.DataRows, d.Resources.DataBytes = d.DataRows, d.DataBytes
	if c, ok := page.comps[d.Component]; ok {
		d.Machines = machinesOf(c)
	}
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
