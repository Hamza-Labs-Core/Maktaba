// Package plugins is the plugin API's rules (Epic 54, story 54.04): invocation tokens
// minted from the grants the API stores, and the checks every plugin call passes
// (token, capability, subject and library scope, quota, rate). The host and the pipeline
// are not trusted to enforce anything: a token minted here is the only thing that opens a
// call, and it is checked again on each one.
package plugins

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

// Refusal codes the plugin API answers (words in web/src/i18n as plugins.error.<code>).
const (
	CodeToken          = "plugin_token"
	CodeGrantsChanged  = "plugin_grants_changed"
	CodeCapability     = "plugin_capability"
	CodeScope          = "plugin_scope"
	CodeField          = "plugin_field"
	CodeQuota          = "plugin_quota"
	CodeRate           = "plugin_rate"
	CodeTooLarge       = "plugin_too_large"
	CodeInvalid        = "plugin_invalid"
	CodeNotFound       = "not_found"
	CodeMediaChanged   = "media_changed"
	CodeMediaGone      = "media_unavailable"
	CodeNotPluginJob   = "plugin_not_plugin_job"
	CodeNotInstalled   = "plugin_not_installed"
	CodeJobNotRunnable = "plugin_job_not_runnable"
)

// Refusal is an answer that is not a success: the HTTP status and the code.
type Refusal struct {
	Status int
	Code   string
	Detail string
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Detail }

func refuse(status int, code, detail string) *Refusal {
	return &Refusal{Status: status, Code: code, Detail: detail}
}

// Grants is a plugin's row of plugin_grants (54.08 §1). A plugin with no row has nothing.
type Grants struct {
	PluginID     string
	Capabilities []string
	Libraries    []string
	AllLibraries bool
	Generation   int64
}

// LoadGrants reads a plugin's grants (zero Grants, generation 0, when it has none).
func LoadGrants(ctx context.Context, db *sql.DB, pluginID string) (Grants, error) {
	g := Grants{PluginID: pluginID}
	var caps, libs pq.StringArray
	err := db.QueryRowContext(ctx, `SELECT capabilities, CAST(libraries AS text[]), all_libraries, generation
		FROM plugin_grants WHERE plugin_id = $1`, pluginID).Scan(&caps, &libs, &g.AllLibraries, &g.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return g, nil
	}
	g.Capabilities, g.Libraries = caps, libs
	return g, err
}

// Invocation is one running plugin job's token, as the API keeps it.
type Invocation struct {
	ID              string
	PluginID        string
	Version         string
	JobID           int64
	Stage           string
	SubjectKind     string
	SubjectID       string
	Libraries       []string
	AllLibraries    bool
	Grants          []string
	GrantGeneration int64
	Exp             time.Time
	// limits from the installed manifest
	DataBytes int64
	// the stages of this plugin, by queue name, and what each may request
	MayRequest map[string][]string
	// Lineage is the job's (54.40 §3): every request and event it makes extends it
	Lineage Lineage
	// counters for the budgets of 54.05 and the rate of 54.04 §5
	mu            sync.Mutex
	ended         bool
	mediaBytes    int64
	frames        int
	audioMs       int64
	requests      int
	bucket        float64
	bucketAt      time.Time
	handles       map[uint64]*handle
	nextHandle    uint64
	refusalsTotal int
}

// Granted reports whether a capability (base name, "plugin-data.read:x" exactly) was granted.
func (inv *Invocation) Granted(capability string) bool {
	for _, g := range inv.Grants {
		if g == capability || strings.SplitN(g, ":", 2)[0] == capability {
			return true
		}
	}
	return false
}

// LibraryAllowed reports whether a library is in the token's scope.
func (inv *Invocation) LibraryAllowed(libraryID string) bool {
	if inv.AllLibraries {
		return libraryID != ""
	}
	for _, l := range inv.Libraries {
		if l == libraryID {
			return true
		}
	}
	return false
}

// Minted is what the mint route answers. The caller cannot pass grants: they come from
// plugin_grants only (54.04 §2).
type Minted struct {
	InvocationID string    `json:"invocation_id"`
	Token        string    `json:"token"`
	PluginID     string    `json:"plugin_id"`
	Version      string    `json:"version"`
	Grants       []string  `json:"grants"`
	Libraries    []string  `json:"libraries"`
	AllLibraries bool      `json:"all_libraries"`
	Exp          time.Time `json:"exp"`
	// Lineage is the job's; the pipeline extends it for each request and event it enqueues
	Lineage Lineage `json:"lineage"`
}

// Tokens keeps the running invocations in memory (a map lookup by the token's sha256)
// with a Postgres copy (plugin_invocations), so a multi-replica API could check them.
type Tokens struct {
	DB  *sql.DB
	Now func() time.Time
	mu  sync.Mutex
	// by sha256 hex of the token
	byHash map[string]*Invocation
	byID   map[string]*Invocation
}

// NewTokens answers an empty token store over db.
func NewTokens(db *sql.DB) *Tokens {
	return &Tokens{DB: db, Now: time.Now, byHash: map[string]*Invocation{}, byID: map[string]*Invocation{}}
}

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// installedManifest is the part of a plugin_installs manifest the API reads.
type installedManifest struct {
	Resources struct {
		TimeoutS int64 `json:"timeout_s"`
		DataMB   int64 `json:"data_mb"`
	} `json:"resources"`
	Stage []struct {
		Name       string   `json:"name"`
		MayRequest []string `json:"may_request"`
	} `json:"stage"`
}

// Mint makes a token for a plugin job (54.04 §2): the job's stage must belong to an
// installed plugin; grants and libraries come from plugin_grants alone.
func (t *Tokens) Mint(ctx context.Context, jobID int64) (*Minted, error) {
	var stage, kind, subject, payload string
	var ext sql.NullString
	err := t.DB.QueryRowContext(ctx, `SELECT j.stage, j.subject_kind, COALESCE(CAST(j.subject_id AS text), ''), s.extension,
		COALESCE(CAST(j.payload AS text), '')
		FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage WHERE j.id = $1`, jobID).Scan(&stage, &kind, &subject, &ext, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, refuse(404, CodeNotFound, "no such job")
	}
	if err != nil {
		return nil, err
	}
	pluginID, ok := strings.CutPrefix(ext.String, "plugin:")
	if !ok || pluginID == "" {
		return nil, refuse(409, CodeNotPluginJob, "the job's stage is not a plugin's")
	}
	var version string
	var raw []byte
	err = t.DB.QueryRowContext(ctx, `SELECT version, manifest FROM plugin_installs WHERE plugin_id = $1`, pluginID).Scan(&version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, refuse(409, CodeNotInstalled, pluginID)
	}
	if err != nil {
		return nil, err
	}
	var m installedManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("plugins: %s manifest: %w", pluginID, err)
	}
	g, err := LoadGrants(ctx, t.DB, pluginID)
	if err != nil {
		return nil, err
	}
	lineage, ref := lineageOf(ctx, t.DB, []byte(payload), stage, kind, subject, t.Now())
	if ref != nil {
		return nil, ref
	}
	timeout := m.Resources.TimeoutS
	if timeout <= 0 {
		timeout = 300
	}
	now := t.Now()
	inv := &Invocation{
		ID:              "inv_" + randomHex(12),
		PluginID:        pluginID,
		Version:         version,
		JobID:           jobID,
		Stage:           stage,
		SubjectKind:     kind,
		SubjectID:       subject,
		Libraries:       g.Libraries,
		AllLibraries:    g.AllLibraries,
		Grants:          g.Capabilities,
		GrantGeneration: g.Generation,
		Exp:             now.Add(time.Duration(timeout)*time.Second + 30*time.Second),
		DataBytes:       m.Resources.DataMB << 20,
		MayRequest:      map[string][]string{},
		Lineage:         lineage,
		bucket:          RateBurst,
		bucketAt:        now,
		handles:         map[uint64]*handle{},
	}
	for _, s := range m.Stage {
		inv.MayRequest[QueueName(pluginID, s.Name)] = queueNames(pluginID, s.MayRequest)
	}
	token := randomHex(32)
	h := hashToken(token)
	if _, err := t.DB.ExecContext(ctx, `INSERT INTO plugin_invocations (id, token_hash, plugin_id, version, job_id, stage,
		subject_kind, subject_id, libraries, all_libraries, grants, grant_generation, exp)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,CAST($9 AS uuid[]),$10,$11,$12,$13)`,
		inv.ID, h, pluginID, version, jobID, stage, kind, subject, pq.Array(nonNil(g.Libraries)), g.AllLibraries,
		pq.Array(nonNil(g.Capabilities)), g.Generation, inv.Exp); err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.byHash[h], t.byID[inv.ID] = inv, inv
	t.mu.Unlock()
	return &Minted{InvocationID: inv.ID, Token: token, PluginID: pluginID, Version: version, Grants: nonNil(g.Capabilities),
		Libraries: nonNil(g.Libraries), AllLibraries: g.AllLibraries, Exp: inv.Exp, Lineage: lineage}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Check answers the invocation of a token, or the refusal: unknown, ended or expired
// tokens are plugin_token; a token whose grants moved since it was minted is
// plugin_grants_changed (a revoked grant applies on the next call, README §4.5).
func (t *Tokens) Check(ctx context.Context, token string) (*Invocation, *Refusal) {
	if token == "" {
		return nil, refuse(401, CodeToken, "no token")
	}
	t.mu.Lock()
	inv := t.byHash[hashToken(token)]
	t.mu.Unlock()
	if inv == nil {
		return nil, refuse(401, CodeToken, "unknown token")
	}
	inv.mu.Lock()
	ended := inv.ended
	inv.mu.Unlock()
	if ended || !t.Now().Before(inv.Exp) {
		return nil, refuse(401, CodeToken, "the token ended")
	}
	var gen int64
	err := t.DB.QueryRowContext(ctx, `SELECT generation FROM plugin_grants WHERE plugin_id = $1`, inv.PluginID).Scan(&gen)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, refuse(503, CodeMediaGone, err.Error())
	}
	if gen != inv.GrantGeneration {
		return nil, refuse(401, CodeGrantsChanged, "the plugin's grants changed")
	}
	return inv, nil
}

// End ends an invocation (the job finished or was cancelled). Answers false if unknown.
func (t *Tokens) End(ctx context.Context, id string) bool {
	ok, _ := t.EndCounting(ctx, id)
	return ok
}

// EndCounting is End, answering also how many of the job's calls were refused (the
// pipeline's circuit breaker counts them, story 54.09 §2).
func (t *Tokens) EndCounting(ctx context.Context, id string) (bool, int64) {
	t.mu.Lock()
	inv := t.byID[id]
	if inv != nil {
		delete(t.byID, id)
		for h, x := range t.byHash {
			if x == inv {
				delete(t.byHash, h)
			}
		}
	}
	t.mu.Unlock()
	if inv == nil {
		return false, 0
	}
	inv.mu.Lock()
	inv.ended = true
	for _, h := range inv.handles {
		h.close()
	}
	inv.handles = map[uint64]*handle{}
	calls, written := inv.refusalsTotal, int64(0)
	inv.mu.Unlock()
	_, _ = t.DB.ExecContext(ctx, `UPDATE plugin_invocations SET ended_at = now(), calls = calls + $2, bytes_written = bytes_written + $3
		WHERE id = $1`, id, calls, written)
	return true, int64(calls)
}

// EndPlugin ends every invocation of a plugin (a drain, an uninstall: 54.07).
func (t *Tokens) EndPlugin(ctx context.Context, pluginID string) int {
	t.mu.Lock()
	var ids []string
	for id, inv := range t.byID {
		if inv.PluginID == pluginID {
			ids = append(ids, id)
		}
	}
	t.mu.Unlock()
	for _, id := range ids {
		t.End(ctx, id)
	}
	return len(ids)
}

// Prune deletes plugin_invocations rows older than 7 days (54.04 §2).
func (t *Tokens) Prune(ctx context.Context) (int64, error) {
	res, err := t.DB.ExecContext(ctx, `DELETE FROM plugin_invocations WHERE created_at < now() - interval '7 days'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// QueueName is a plugin stage's job_stages.name (54.01 §4).
func QueueName(pluginID, stage string) string {
	s := sha256.Sum256([]byte(pluginID))
	return "p_" + hex.EncodeToString(s[:])[:8] + "_" + stage
}

func queueNames(pluginID string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = QueueName(pluginID, n)
	}
	return out
}
