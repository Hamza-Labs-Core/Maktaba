package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// The plugins' lifecycle on Epic 51 (stories 54.07, 54.08, 54.10): install and update from
// a package, grants, the home's policy, keep-off, rollback, purge and the writes' revert.
// Every switch of a plugin goes through the components service (svc.Service.Apply), so
// /api/plugins/* and /api/components/plugin.<id> write the same rows and audit actions;
// Lifecycle is that service's PluginHooks.

// Problem codes of the lifecycle (each has words, plugins.error.<code>).
const (
	CodeUnknownCapability  = "plugin_unknown_capability"
	CodeRequiredCapability = "plugin_required_capability"
	CodeUnknownLibrary     = "plugin_unknown_library"
	CodeUnreviewed         = "plugin_unreviewed_disallowed"
	CodeWrongPackage       = "plugin_wrong_package"
	CodeAlreadyInstalled   = "already_installed"
	CodeNoPrevious         = "plugin_no_previous"
	CodeDataNewer          = "plugin_data_newer"
	CodeRevoked            = "plugin_revoked"
	CodeNoSupervisor       = "plugins_unavailable"
	CodeNameMismatch       = "plugin_name_mismatch"
)

// Who holds a plugin when it is not an admin.
const (
	HoldBreaker = "plugin-breaker"
	HoldPolicy  = "unreviewed-policy"
)

// Problem is a refusal of the lifecycle: its status, code and detail.
type Problem struct {
	Status int
	Code   string
	Detail string
	Params map[string]any
}

func (p *Problem) Error() string { return p.Code + ": " + p.Detail }

func problem(status int, code, format string, args ...any) *Problem {
	return &Problem{Status: status, Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Installed is the supervisor's answer to an install (cmd/maktaba-server/internal/plugins).
type Installed struct {
	PluginID string          `json:"plugin_id"`
	Version  string          `json:"version"`
	Digest   string          `json:"digest"`
	Folder   string          `json:"folder"`
	Path     string          `json:"path"`
	Manifest json.RawMessage `json:"manifest"`
	Signed   bool            `json:"signed"`
	Already  bool            `json:"already"`
}

// Supervisor is this home's supervisor as the lifecycle uses it: it puts a package on
// disk (the API never writes a plugin folder, story 54.38 §4).
type Supervisor interface {
	InstallPackage(ctx context.Context, r io.Reader) (*Installed, error)
	Discard(ctx context.Context, id, folder string) error
}

// Lifecycle is the plugins' lifecycle on the components service.
type Lifecycle struct {
	DB         *sql.DB
	Components *svc.Service
	Sup        Supervisor
	// Version is this server's (a manifest's maktaba range is checked against it).
	Version string
	// ArtifactsDir is <data>/plugins/artifacts (a purge deletes the plugin's there).
	ArtifactsDir string
	Logger       *slog.Logger
}

func (l *Lifecycle) logger() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.Default()
}

// manifestOf is what the lifecycle reads of a normalised manifest.
type manifestOf struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	Maktaba      string `json:"maktaba"`
	PluginAPI    string `json:"plugin_api"`
	DataVersion  int    `json:"data_version"`
	Capabilities struct {
		Required []string `json:"required"`
		Optional []string `json:"optional"`
	} `json:"capabilities"`
	Resources struct {
		TimeoutS    int `json:"timeout_s"`
		Concurrency int `json:"concurrency"`
		MemoryMB    int `json:"memory_mb"`
		DataMB      int `json:"data_mb"`
	} `json:"resources"`
	Stage []struct {
		Name    string `json:"name"`
		Subject string `json:"subject"`
		Version int    `json:"version"`
		Trigger []struct {
			Event string `json:"event"`
		} `json:"trigger"`
	} `json:"stage"`
	Platforms []string `json:"platforms"`
	Licence   string   `json:"licence"`
	Strings   struct {
		Name    string `json:"name"`
		Summary string `json:"summary"`
	} `json:"strings"`
}

func parseManifest(raw []byte) (manifestOf, error) {
	var m manifestOf
	err := json.Unmarshal(raw, &m)
	return m, err
}

func (m manifestOf) asked() []string {
	return append(slices.Clone(m.Capabilities.Required), m.Capabilities.Optional...)
}

// installRow is a plugin_installs row.
type installRow struct {
	ID, Version, Digest, Path, ReviewLevel string
	Manifest                               json.RawMessage
	InstalledAt                            time.Time
	PrevVersion, PrevDigest, PrevPath      sql.NullString
	PrevManifest                           []byte
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func loadInstall(ctx context.Context, q querier, id string, forUpdate bool) (*installRow, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	var r installRow
	err := q.QueryRowContext(ctx, `SELECT plugin_id, version, digest, path, manifest, review_level, installed_at,
		previous_version, previous_digest, previous_path, previous_manifest FROM plugin_installs WHERE plugin_id = $1`+lock, id).
		Scan(&r.ID, &r.Version, &r.Digest, &r.Path, &r.Manifest, &r.ReviewLevel, &r.InstalledAt,
			&r.PrevVersion, &r.PrevDigest, &r.PrevPath, &r.PrevManifest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Grants are a plugin's plugin_grants row.
type GrantSet struct {
	Capabilities []string  `json:"capabilities"`
	Libraries    []string  `json:"libraries"`
	AllLibraries bool      `json:"all_libraries"`
	Generation   int64     `json:"generation"`
	GrantedBy    string    `json:"granted_by"`
	GrantedAt    time.Time `json:"granted_at"`
}

func loadGrantSet(ctx context.Context, q querier, id string) (*GrantSet, error) {
	g := GrantSet{}
	err := q.QueryRowContext(ctx, `SELECT capabilities, CAST(libraries AS text[]), all_libraries, generation, granted_by, granted_at
		FROM plugin_grants WHERE plugin_id = $1`, id).
		Scan(pq.Array(&g.Capabilities), pq.Array(&g.Libraries), &g.AllLibraries, &g.Generation, &g.GrantedBy, &g.GrantedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	g.Capabilities, g.Libraries = nonNil(g.Capabilities), nonNil(g.Libraries)
	return &g, nil
}

// Policy is the home's plugin policy (app_settings plugins.policy).
type Policy struct {
	AllowUnreviewed bool `json:"allow_unreviewed"`
}

// PolicyKey is the policy's app_settings key (the supervisor's runner reads the same).
const PolicyKey = "plugins.policy"

// LoadPolicy reads the policy (its defaults when none was set).
func LoadPolicy(ctx context.Context, q querier) (Policy, error) {
	var raw []byte
	var p Policy
	err := q.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, PolicyKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(raw, &p)
	return p, err
}

// audit writes one audit_log row of the components category (action plugin.<verb>).
func audit(ctx context.Context, tx *sql.Tx, action, id string, actor svc.Actor, before, after any, extra map[string]any) error {
	payload := map[string]any{"before": before, "after": after, "by": actor.By}
	for k, v := range extra {
		payload[k] = v
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var actorID any
	if _, perr := uuid.Parse(actor.UserID); perr == nil {
		actorID = actor.UserID
	}
	source := actor.Source
	if source == "" {
		source = "admin"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id, target_kind,
		actor_source, payload) VALUES ($1, $2, $3, $4, 'plugin', $5, $6)`, store.AuditCategory, action, actorID, id, source, string(b))
	return err
}

// touch moves a plugin's desired row to a new generation without changing its want (a
// grant, the policy or keep-off changed: story 54.37 §3), so every runner hears
// components_changed and recomputes its gates.
func touch(ctx context.Context, q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}, pluginID, by string) error {
	_, err := q.ExecContext(ctx, `UPDATE components SET updated_by = $2 WHERE id = $1`, components.PluginPrefix+pluginID, by)
	return err
}

// ---- the components service's hooks -------------------------------------------------

// Check refuses an enable whose required capabilities are not all granted (story 54.08
// §1: plugin_required_capability) or of a plugin no longer installed.
func (l *Lifecycle) Check(ctx context.Context, ch svc.Change) error {
	if ch.Want != components.WantEnabled {
		return nil
	}
	id := strings.TrimPrefix(ch.ID, components.PluginPrefix)
	in, err := loadInstall(ctx, l.DB, id, false)
	if err != nil {
		return err
	}
	if in == nil {
		return problem(http.StatusConflict, CodeNotInstalled, "%s is not installed", id)
	}
	m, err := parseManifest(in.Manifest)
	if err != nil {
		return err
	}
	g, err := loadGrantSet(ctx, l.DB, id)
	if err != nil {
		return err
	}
	var granted []string
	if g != nil {
		granted = g.Capabilities
	}
	if missing := missingOf(m.Capabilities.Required, granted); len(missing) > 0 {
		p := problem(http.StatusBadRequest, CodeRequiredCapability, "grant %s first", strings.Join(missing, ", "))
		p.Params = map[string]any{"capabilities": missing}
		return p
	}
	return nil
}

func missingOf(required, granted []string) []string {
	var out []string
	for _, c := range required {
		if !slices.Contains(granted, c) {
			out = append(out, c)
		}
	}
	return out
}

// Written is a plugin row's part of a batch, in its transaction (story 54.07 §1): an
// admin's disable (want installed) sets keep-off, an enable clears it; a hold released
// clears a tripped breaker's window (54.09 §2: only an admin's Start resets it).
func (l *Lifecycle) Written(ctx context.Context, tx *sql.Tx, after store.Desired, actor svc.Actor) error {
	id := strings.TrimPrefix(after.ID, components.PluginPrefix)
	switch after.Want {
	case components.WantInstalled:
		if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_keep_off (plugin_id, set_by) VALUES ($1, $2)
			ON CONFLICT (plugin_id) DO NOTHING`, id, actor.By); err != nil {
			return err
		}
	case components.WantEnabled:
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_keep_off WHERE plugin_id = $1`, id); err != nil {
			return err
		}
	}
	if after.Hold == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_health SET tripped = false, tripped_at = NULL, trip_reason = '',
			recent = '[]'::jsonb, crashes = '[]'::jsonb, refusals = '[]'::jsonb, updated_at = now()
			WHERE plugin_id = $1 AND tripped`, id); err != nil {
			return err
		}
	}
	return nil
}

// ---- install and update -------------------------------------------------------------

// InstallRequest is POST /api/plugins/install (and /update).
type InstallRequest struct {
	Package io.Reader
	// Grants are the capabilities the admin ticks (nil: none on a new install, the
	// existing ones on an update); Libraries the libraries (none unless ticked, 54.08 §1).
	Grants       []string
	Libraries    []string
	AllLibraries bool
	// GrantsSet: Grants and the libraries were sent (an update keeps them otherwise).
	GrantsSet bool
	// Enable: "Install and turn on".
	Enable bool
	// ExpectID, when set, is the plugin the package must be (an update of that plugin).
	ExpectID string
	// ConfirmName is the plugin's name as the admin typed it, in English or Arabic: an
	// unreviewed plugin installs only with it (54.10 §3).
	ConfirmName string
	// ReviewLevel is the package's: none, developer or development (Phase 1: a file is
	// never store-reviewed).
	ReviewLevel string
}

// InstallResult is what an install did.
type InstallResult struct {
	PluginID string `json:"plugin_id"`
	Version  string `json:"version"`
	// Already: this version and digest were installed; nothing changed.
	Already bool `json:"already_installed"`
	// Updated: another version was installed before (it is the previous one now).
	Updated bool `json:"updated"`
	// KeptOff is when an admin turned the plugin off (it installs off).
	KeptOff *time.Time `json:"kept_off,omitempty"`
	// Enabled is whether it is on now; EnableError why "Install and turn on" did not.
	Enabled     bool   `json:"enabled"`
	EnableError string `json:"enable_error,omitempty"`
}

// Install puts a package on this home (story 54.07 §4): the supervisor unpacks, validates
// and preloads it; the grants are checked against its manifest; plugin_installs, the
// grants, the desired row (want installed) and the audit rows are written in one
// transaction; the plugin is enabled only when asked and not kept off.
func (l *Lifecycle) Install(ctx context.Context, req InstallRequest, actor svc.Actor) (*InstallResult, error) {
	if l.Sup == nil {
		return nil, problem(http.StatusServiceUnavailable, CodeNoSupervisor, "this server runs under no supervisor")
	}
	level := req.ReviewLevel
	if level == "" {
		level = "none"
	}
	pol, err := LoadPolicy(ctx, l.DB)
	if err != nil {
		return nil, err
	}
	if components.Unreviewed(level) && !pol.AllowUnreviewed {
		return nil, problem(http.StatusBadRequest, CodeUnreviewed,
			"turn on Allow unreviewed plugins (Settings, Plugins) to install a file no store reviewed")
	}
	in, err := l.Sup.InstallPackage(ctx, req.Package)
	if err != nil {
		return nil, err
	}
	placed := !in.Already
	ok := false
	defer func() {
		if !ok && placed {
			if err := l.Sup.Discard(context.WithoutCancel(ctx), in.PluginID, in.Folder); err != nil {
				l.logger().Warn("plugin_discard_failed", "plugin", in.PluginID, "folder", in.Folder, "err", err)
			}
		}
	}()
	if req.ExpectID != "" && in.PluginID != req.ExpectID {
		return nil, problem(http.StatusBadRequest, CodeWrongPackage, "the package is %s, not %s", in.PluginID, req.ExpectID)
	}
	m, err := parseManifest(in.Manifest)
	if err != nil {
		return nil, err
	}
	if components.Unreviewed(level) {
		// an unreviewed plugin is confirmed by typing its name (story 54.10 §3)
		name := DisplayName(in.Path, m.Strings.Name, "en")
		ar := DisplayName(in.Path, m.Strings.Name, "ar")
		typed := strings.TrimSpace(req.ConfirmName)
		if typed == "" || (!strings.EqualFold(typed, name) && typed != ar) {
			p := problem(http.StatusBadRequest, CodeNameMismatch, "type the plugin's name, %q, to confirm", name)
			p.Params = map[string]any{"name": name, "name_ar": ar}
			return nil, p
		}
	}
	if err := checkAsked(req.Grants, m); err != nil {
		return nil, err
	}
	if err := l.checkLibraries(ctx, req.Libraries); err != nil {
		return nil, err
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	old, err := loadInstall(ctx, tx, in.PluginID, true)
	if err != nil {
		return nil, err
	}
	res := &InstallResult{PluginID: in.PluginID, Version: in.Version}
	if old != nil && old.Version == in.Version && old.Digest == in.Digest {
		res.Already, ok = true, true
		placed = false
		res.KeptOff = l.keptOff(ctx, in.PluginID)
		res.Enabled = l.enabled(ctx, in.PluginID)
		return res, nil
	}
	if old != nil && req.ExpectID == "" && placed {
		// an install of another version over this plugin is its update
		res.Updated = true
	}
	if old != nil {
		res.Updated = true
	}
	prevGrants, err := loadGrantSet(ctx, tx, in.PluginID)
	if err != nil {
		return nil, err
	}
	grants := GrantSet{Capabilities: nonNil(req.Grants), Libraries: nonNil(req.Libraries), AllLibraries: req.AllLibraries}
	if !req.GrantsSet && prevGrants != nil {
		// an update keeps what was granted, less what the new version no longer asks for
		grants = GrantSet{Libraries: prevGrants.Libraries, AllLibraries: prevGrants.AllLibraries}
		for _, c := range prevGrants.Capabilities {
			if slices.Contains(m.asked(), c) {
				grants.Capabilities = append(grants.Capabilities, c)
			}
		}
		grants.Capabilities = nonNil(grants.Capabilities)
	}
	wasOn := old != nil && l.enabled(ctx, in.PluginID)
	if missing := missingOf(m.Capabilities.Required, grants.Capabilities); len(missing) > 0 && (req.Enable || wasOn) {
		// the admin approves a new required capability with the update (Phase 1: the dialog
		// asks for it before the update, so the running version is never left waiting)
		p := problem(http.StatusBadRequest, CodeRequiredCapability, "grant %s with it", strings.Join(missing, ", "))
		p.Params = map[string]any{"capabilities": missing}
		return nil, p
	}
	if old == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest, review_level)
			VALUES ($1, $2, $3, $4, $5, $6)`, in.PluginID, in.Version, in.Digest, in.Path, string(in.Manifest), level)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE plugin_installs SET previous_version = version, previous_digest = digest,
			previous_path = path, previous_manifest = manifest, version = $2, digest = $3, path = $4, manifest = $5,
			review_level = $6, installed_at = now() WHERE plugin_id = $1`,
			in.PluginID, in.Version, in.Digest, in.Path, string(in.Manifest), level)
	}
	if err != nil {
		return nil, err
	}
	if err := writeGrants(ctx, tx, in.PluginID, grants, actor.By); err != nil {
		return nil, err
	}
	cur, err := desiredOf(ctx, tx, components.PluginPrefix+in.PluginID)
	if err != nil {
		return nil, err
	}
	if cur == nil || cur.Want == components.WantAbsent {
		if _, err := svc.SetDesiredTx(ctx, l.Components.Store, tx, store.Desired{ID: components.PluginPrefix + in.PluginID,
			Want: components.WantInstalled}, store.Audit{By: actor.By, ActorUserID: actor.UserID, ActorSource: sourceOf(actor),
			Action: "component.install"}); err != nil {
			return nil, err
		}
	} else if err := touch(ctx, tx, in.PluginID, actor.By); err != nil {
		return nil, err
	}
	action := "plugin.install"
	var before any
	if old != nil {
		action, before = "plugin.update", map[string]any{"version": old.Version, "digest": old.Digest}
	}
	if err := audit(ctx, tx, action, components.PluginPrefix+in.PluginID, actor, before,
		map[string]any{"version": in.Version, "digest": in.Digest, "review_level": level, "grants": grants}, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	ok = true
	if err := ReloadCatalog(ctx, l.DB, l.Logger); err != nil {
		return nil, err
	}
	if old != nil {
		l.carrySettings(ctx, in.PluginID, actor)
	}
	res.KeptOff = l.keptOff(ctx, in.PluginID)
	res.Enabled = wasOn
	if req.Enable && res.KeptOff == nil && !wasOn {
		if _, err := l.Components.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: components.PluginPrefix + in.PluginID,
			Want: components.WantEnabled}}}, actor); err != nil {
			res.EnableError = err.Error()
			var ref *svc.Refusal
			if errors.As(err, &ref) {
				res.EnableError = ref.Code
			}
		} else {
			res.Enabled = true
		}
	}
	return res, nil
}

func sourceOf(a svc.Actor) string {
	if a.Source == "" {
		return "admin"
	}
	return a.Source
}

func desiredOf(ctx context.Context, q querier, id string) (*store.Desired, error) {
	var d store.Desired
	var holdBy sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id, want, generation, hold_by FROM components WHERE id = $1`, id).
		Scan(&d.ID, &d.Want, &d.Generation, &holdBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if holdBy.Valid {
		d.Hold = &store.Hold{By: holdBy.String}
	}
	return &d, nil
}

// checkAsked refuses a grant the manifest does not ask for (story 54.08 §1).
func checkAsked(grants []string, m manifestOf) error {
	asked := m.asked()
	for _, g := range grants {
		if !slices.Contains(asked, g) {
			return problem(http.StatusBadRequest, CodeUnknownCapability, "%s is not asked for by the plugin", g)
		}
	}
	return nil
}

func (l *Lifecycle) checkLibraries(ctx context.Context, libs []string) error {
	for _, id := range libs {
		if _, err := uuid.Parse(id); err != nil {
			return problem(http.StatusBadRequest, CodeUnknownLibrary, "%q is not a library id", id)
		}
	}
	if len(libs) == 0 {
		return nil
	}
	var n int
	if err := l.DB.QueryRowContext(ctx, `SELECT count(*) FROM libraries WHERE id = ANY(CAST($1 AS uuid[]))`, pq.Array(libs)).Scan(&n); err != nil {
		return err
	}
	if n != len(dedupe(libs)) {
		return problem(http.StatusBadRequest, CodeUnknownLibrary, "a library does not exist")
	}
	return nil
}

func dedupe(s []string) []string {
	out := []string{}
	for _, x := range s {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// writeGrants upserts a plugin's grants, the generation moving on a change.
func writeGrants(ctx context.Context, tx *sql.Tx, id string, g GrantSet, by string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO plugin_grants (plugin_id, capabilities, libraries, all_libraries, granted_by)
		VALUES ($1, $2, CAST($3 AS uuid[]), $4, $5)
		ON CONFLICT (plugin_id) DO UPDATE SET capabilities = excluded.capabilities, libraries = excluded.libraries,
			all_libraries = excluded.all_libraries, granted_by = excluded.granted_by, granted_at = now(),
			generation = plugin_grants.generation + 1
		WHERE plugin_grants.capabilities IS DISTINCT FROM excluded.capabilities
		   OR plugin_grants.libraries IS DISTINCT FROM excluded.libraries
		   OR plugin_grants.all_libraries IS DISTINCT FROM excluded.all_libraries`,
		id, pq.Array(dedupe(g.Capabilities)), pq.Array(dedupe(g.Libraries)), g.AllLibraries, by)
	return err
}

func (l *Lifecycle) keptOff(ctx context.Context, id string) *time.Time {
	var at time.Time
	if err := l.DB.QueryRowContext(ctx, `SELECT set_at FROM plugin_keep_off WHERE plugin_id = $1`, id).Scan(&at); err != nil {
		return nil
	}
	return &at
}

func (l *Lifecycle) enabled(ctx context.Context, id string) bool {
	d, err := desiredOf(ctx, l.DB, components.PluginPrefix+id)
	return err == nil && d != nil && d.Want == components.WantEnabled
}

// ---- grants and policy --------------------------------------------------------------

// SetGrants changes a plugin's grants and libraries (story 54.08 §1): each capability must
// be one the installed version asks for; the generation moves (a token minted before
// answers plugin_grants_changed), an audit row has the before and after, and the plugin's
// desired row moves so every runner recomputes its gates.
func (l *Lifecycle) SetGrants(ctx context.Context, id string, g GrantSet, actor svc.Actor) (*GrantSet, error) {
	in, err := loadInstall(ctx, l.DB, id, false)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, problem(http.StatusNotFound, CodeNotInstalled, "%s is not installed", id)
	}
	m, err := parseManifest(in.Manifest)
	if err != nil {
		return nil, err
	}
	if err := checkAsked(g.Capabilities, m); err != nil {
		return nil, err
	}
	if err := l.checkLibraries(ctx, g.Libraries); err != nil {
		return nil, err
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	before, err := loadGrantSet(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := writeGrants(ctx, tx, id, g, actor.By); err != nil {
		return nil, err
	}
	after, err := loadGrantSet(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if before == nil || before.Generation != after.Generation {
		if err := audit(ctx, tx, "plugin.grants", components.PluginPrefix+id, actor, before, after, nil); err != nil {
			return nil, err
		}
		if err := touch(ctx, tx, id, actor.By); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return after, nil
}

// SetPolicy writes the home's policy (story 54.10 §2), audited. Turned off, every enabled
// unreviewed plugin is also held (hold by unreviewed-policy, component.hold): turning it on
// again lifts the gate, not the holds. Every plugin's desired row moves.
func (l *Lifecycle) SetPolicy(ctx context.Context, p Policy, actor svc.Actor) (Policy, error) {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer func() { _ = tx.Rollback() }()
	before, err := LoadPolicy(ctx, tx)
	if err != nil {
		return p, err
	}
	b, _ := json.Marshal(p)
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()`, PolicyKey, string(b)); err != nil {
		return p, err
	}
	if before != p {
		if err := audit(ctx, tx, "plugin.policy", PolicyKey, actor, before, p, nil); err != nil {
			return p, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE components SET updated_by = $1 WHERE id LIKE 'plugin.%'`, actor.By); err != nil {
			return p, err
		}
	}
	if err := tx.Commit(); err != nil {
		return p, err
	}
	if before.AllowUnreviewed && !p.AllowUnreviewed {
		l.holdUnreviewed(ctx)
	}
	return p, nil
}

// holdUnreviewed holds every enabled unreviewed plugin (the policy turned off).
func (l *Lifecycle) holdUnreviewed(ctx context.Context) {
	rows, err := l.DB.QueryContext(ctx, `SELECT i.plugin_id FROM plugin_installs i JOIN components c ON c.id = 'plugin.' || i.plugin_id
		WHERE c.want = 'enabled' AND c.hold_by IS NULL AND i.review_level <> 'store'`)
	if err != nil {
		l.logger().Warn("plugin_policy_hold_failed", "err", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	held := true
	for _, id := range ids {
		if _, err := l.Components.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: components.PluginPrefix + id, Hold: &held}}},
			svc.Actor{By: HoldPolicy, Source: "system"}); err != nil {
			l.logger().Warn("plugin_policy_hold_failed", "plugin", id, "err", err)
		}
	}
}

// ---- rollback -----------------------------------------------------------------------

// Rollback switches a plugin back to its kept previous version (story 54.07 §6). A
// previous version whose data_version is lower than the data's is refused
// (plugin_data_newer) unless clearData: then the plugin's data is deleted (the tags,
// chapters and subtitles it wrote are kept). The supervisor follows the pointer.
func (l *Lifecycle) Rollback(ctx context.Context, id string, clearData bool, actor svc.Actor) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	in, err := loadInstall(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if in == nil {
		return problem(http.StatusNotFound, CodeNotInstalled, "%s is not installed", id)
	}
	if !in.PrevPath.Valid || in.PrevPath.String == "" || !dirExists(in.PrevPath.String) {
		return problem(http.StatusConflict, CodeNoPrevious, "no previous version of %s is kept", id)
	}
	cur, err := parseManifest(in.Manifest)
	if err != nil {
		return err
	}
	prev, err := parseManifest(in.PrevManifest)
	if err != nil {
		return err
	}
	if prev.DataVersion < cur.DataVersion {
		if !clearData {
			p := problem(http.StatusConflict, CodeDataNewer, "the data is version %d; %s keeps %d", cur.DataVersion,
				prev.Version, prev.DataVersion)
			p.Params = map[string]any{"data_version": cur.DataVersion, "previous": prev.DataVersion}
			return p
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_data WHERE plugin_id = $1`, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_installs SET version = previous_version, digest = previous_digest,
		path = previous_path, manifest = previous_manifest, previous_version = version, previous_digest = digest,
		previous_path = path, previous_manifest = manifest, installed_at = now() WHERE plugin_id = $1`, id); err != nil {
		return err
	}
	if err := audit(ctx, tx, "plugin.rollback", components.PluginPrefix+id, actor, map[string]any{"version": in.Version},
		map[string]any{"version": in.PrevVersion.String}, map[string]any{"clear_data": clearData}); err != nil {
		return err
	}
	if err := touch(ctx, tx, id, actor.By); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return ReloadCatalog(ctx, l.DB, l.Logger)
}

// ---- the breaker --------------------------------------------------------------------

// Trip holds a plugin its dispatcher's circuit breaker tripped (story 54.09 §2): an Epic 51
// hold by plugin-breaker (component.hold) and one notice to admins. A plugin held already
// is left as it is.
func (l *Lifecycle) Trip(ctx context.Context, id, reason string) (bool, error) {
	d, err := desiredOf(ctx, l.DB, components.PluginPrefix+id)
	if err != nil {
		return false, err
	}
	if d == nil {
		return false, problem(http.StatusNotFound, CodeNotInstalled, "%s is not installed", id)
	}
	if d.Hold != nil {
		return false, nil
	}
	held := true
	if _, err := l.Components.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: components.PluginPrefix + id, Hold: &held}}},
		svc.Actor{By: HoldBreaker, Source: "system"}); err != nil {
		return false, err
	}
	params, _ := json.Marshal(map[string]any{"plugin": id, "reason": reason})
	if _, err := l.DB.ExecContext(ctx, `INSERT INTO notifications (kind, severity, title_key, params, link, audience, subject)
		VALUES ('plugin', 'warn', 'notify.plugins.breaker', $1, '/settings/plugins', 'admins', $2)`, string(params), id); err != nil {
		l.logger().Warn("plugin_breaker_notice_failed", "plugin", id, "err", err)
	}
	l.logger().Warn("plugin_breaker_tripped", "plugin", id, "reason", reason, "event", "plugin_breaker_tripped")
	return true, nil
}
