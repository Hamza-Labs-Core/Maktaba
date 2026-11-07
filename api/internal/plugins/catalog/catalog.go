// Package catalog is the home's side of the plugin store (Epic 54, story 54.17): the loop
// `api:plugins_catalog` that fetches the signed lists from the store's CDN, verifies them by
// the chain of trust (story 54.14, shared/plugins/pluginsig), keeps them and the newest
// versions seen (in <data>/state and in Postgres, the newer of the two winning), applies the
// revocation list to the installed plugins (the kill switch), offers updates, and installs
// from the store.
//
// This is the server reaching the internet for itself, straight to plugins-cdn.maktaba.tv:
// nothing here goes through Maktaba Cloud or needs the home to be linked to it, and requests
// for the public files carry nothing that identifies the home.
package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// DefaultCDN is the store's CDN (MAKTABA_PLUGIN_CDN_URL overrides it: a local store in tests).
const DefaultCDN = "https://plugins-cdn.maktaba.tv"

// HoldRevoked is the Epic 51 hold a store `disable` sets (54.17 §3).
const HoldRevoked = "store-revocation"

// Codes the routes and the log name (words in web/src/i18n as plugins.error.<code>).
const (
	CodeRevoked        = "plugin_revoked"
	CodeDelisted       = "plugin_delisted"
	CodeListExpired    = "store_list_stale"
	CodeNotInCatalog   = "plugin_not_in_catalog"
	CodeDownloadFailed = "plugin_download_failed"
)

// Timings (54.17 §1).
const (
	HeadEvery    = 5 * time.Minute
	HeadJitter   = 30 * time.Second
	ListEvery    = time.Hour
	CatalogEvery = 6 * time.Hour
	BrowseFresh  = time.Hour
	ActiveFor    = 7 * 24 * time.Hour
)

// Store is the store's files as the home reads them (HTTP on the CDN; a fake in tests).
type Store interface {
	Get(ctx context.Context, name string) ([]byte, error)
}

// HTTPStore reads the CDN over HTTPS, a plain GET with only the server version in the
// User-Agent.
type HTTPStore struct {
	Base    string
	Version string
	Client  *http.Client
}

func (h *HTTPStore) Get(ctx context.Context, name string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(h.Base, "/")+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Maktaba/"+h.Version)
	c := h.Client
	if c == nil {
		c = &http.Client{Timeout: 2 * time.Minute}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: the CDN answered %d", name, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 60<<20))
}

// Service keeps the lists and applies them.
type Service struct {
	DB       *sql.DB
	Store    Store
	StateDir string
	Root     *pluginsig.Root
	Now      func() time.Time
	Logger   *slog.Logger
	// Hold puts or lifts an Epic 51 hold on a plugin's component (the components service).
	Hold func(ctx context.Context, id string, held bool, by string) error
	// APIMajor is the plugin API major this release serves.
	APIMajor int64
	// Seen sends the store the installed plugins (54.15 §5, §10: once a day, only for a home
	// linked to a store account and only with telemetry on; nil: never).
	Seen func(ctx context.Context, plugins []SeenPlugin) error

	auto *Auto

	mu       sync.Mutex
	keys     *pluginsig.StoreKeys
	revoked  *pluginsig.Revoked
	catalog  *pluginsig.Catalog
	lastHead time.Time
	lastList time.Time
	lastCat  time.Time
	lastSeen time.Time
	lastErr  string
}

// SeenPlugin is one installed plugin in the seen report.
type SeenPlugin struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// SeenEvery is how often a linked home reports what it has installed.
const SeenEvery = 24 * time.Hour

func (s *Service) seen(ctx context.Context) {
	if s.Seen == nil {
		return
	}
	s.mu.Lock()
	due := s.now().Sub(s.lastSeen) >= SeenEvery
	s.mu.Unlock()
	if !due {
		return
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT plugin_id, version FROM plugin_installs ORDER BY plugin_id`)
	if err != nil {
		return
	}
	list := []SeenPlugin{}
	for rows.Next() {
		var p SeenPlugin
		if rows.Scan(&p.ID, &p.Version) == nil {
			list = append(list, p)
		}
	}
	rows.Close()
	if err := s.Seen(ctx, list); err != nil {
		s.log().InfoContext(ctx, "plugins: the seen report was not sent", "event", "plugin_seen_failed", "err", err)
		return
	}
	s.mu.Lock()
	s.lastSeen = s.now()
	s.mu.Unlock()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) major() int64 {
	if s.APIMajor > 0 {
		return s.APIMajor
	}
	return components.PluginAPIMajor
}

func (s *Service) root() (*pluginsig.Root, error) {
	if s.Root != nil {
		return s.Root, nil
	}
	return pluginsig.EmbeddedRoot()
}

// ListsDir is where the lists are kept on disk (the supervisor's install reads store-keys.json
// there too).
func ListsDir(stateDir string) string { return filepath.Join(stateDir, "plugins-lists") }

// ---- the state: the newest versions seen, in the file and in Postgres ------------------

// State is the newer of the file's and Postgres's copies.
func (s *Service) State(ctx context.Context) pluginsig.State {
	st, _ := pluginsig.LoadState(pluginsig.TrustFile(s.StateDir))
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT state FROM plugin_trust WHERE id = 1`).Scan(&raw); err == nil {
		var db pluginsig.State
		if json.Unmarshal(raw, &db) == nil {
			st.Merge(db)
		}
	}
	return st
}

func (s *Service) saveState(ctx context.Context, st pluginsig.State) error {
	if err := pluginsig.SaveState(pluginsig.TrustFile(s.StateDir), st); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO plugin_trust (id, state, updated_at) VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET state = EXCLUDED.state, updated_at = now()`, string(b))
	return err
}

func (s *Service) saveList(ctx context.Context, name string, body []byte, version int64) error {
	if err := os.MkdirAll(ListsDir(s.StateDir), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(ListsDir(s.StateDir), "."+name+".tmp")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(ListsDir(s.StateDir), name)); err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO plugin_lists (name, body, version, sha256, fetched_at) VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (name) DO UPDATE SET body = EXCLUDED.body, version = EXCLUDED.version, sha256 = EXCLUDED.sha256, fetched_at = now()`,
		name, string(body), version, hex.EncodeToString(sum[:]))
	return err
}

func (s *Service) storedList(ctx context.Context, name string) []byte {
	var body string
	if err := s.DB.QueryRowContext(ctx, `SELECT body FROM plugin_lists WHERE name = $1`, name).Scan(&body); err == nil {
		return []byte(body)
	}
	b, _ := os.ReadFile(filepath.Join(ListsDir(s.StateDir), name))
	return b
}

// Load reads the kept lists (and the release's embedded ones) at start: the newest valid
// of each, and applies the revocation list.
func (s *Service) Load(ctx context.Context) error {
	root, err := s.root()
	if err != nil {
		return err
	}
	st := s.State(ctx)
	var keys *pluginsig.StoreKeys
	for _, raw := range [][]byte{s.storedList(ctx, "store-keys.json"), pluginsig.EmbeddedStoreKeys()} {
		if len(raw) == 0 || root.Empty() {
			continue
		}
		if k, err := root.VerifyStoreKeys(raw, st, s.now()); err == nil && (keys == nil || k.Version > keys.Version) {
			keys = k
		}
	}
	s.mu.Lock()
	s.keys = keys
	s.mu.Unlock()
	if keys == nil {
		return nil
	}
	var stored, embedded *pluginsig.Revoked
	if raw := s.storedList(ctx, "revoked.json"); len(raw) > 0 {
		stored, _ = keys.LoadStoredRevoked(raw)
	}
	if raw := pluginsig.EmbeddedRevoked(); len(raw) > 0 {
		embedded, _ = keys.LoadStoredRevoked(raw)
	}
	if r := pluginsig.NewestRevoked(stored, embedded); r != nil {
		if r.Version >= st.RevokedVersion {
			s.mu.Lock()
			s.revoked = r
			s.mu.Unlock()
			if r == embedded && (stored == nil || embedded.Version > stored.Version) {
				// the release's list is newer than the one kept: it becomes the kept one
				st.AcceptRevoked(r)
				_ = s.saveList(ctx, "revoked.json", r.Raw, r.Version)
				_ = s.saveState(ctx, st)
			}
		}
	}
	if raw := s.storedList(ctx, fmt.Sprintf("catalog-%d.json", s.major())); len(raw) > 0 {
		if c, err := keys.LoadStoredCatalog(raw, s.major()); err == nil {
			s.mu.Lock()
			s.catalog = c
			s.mu.Unlock()
		}
	}
	return s.Apply(ctx)
}

// ---- fetching (54.17 §1) ------------------------------------------------------------

// refreshKeys fetches store-keys.json (with every list fetch); a newer valid one is kept.
func (s *Service) refreshKeys(ctx context.Context, st *pluginsig.State) (*pluginsig.StoreKeys, error) {
	root, err := s.root()
	if err != nil {
		return nil, err
	}
	raw, err := s.Store.Get(ctx, "store-keys.json")
	if err == nil {
		k, verr := root.VerifyStoreKeys(raw, *st, s.now())
		if verr == nil {
			st.AcceptStoreKeys(k)
			if err := s.saveList(ctx, "store-keys.json", raw, k.Version); err != nil {
				return nil, err
			}
			s.mu.Lock()
			s.keys = k
			s.mu.Unlock()
			return k, nil
		}
		code, _ := pluginsig.CodeOf(verr)
		s.log().WarnContext(ctx, "plugins: store-keys.json refused", "event", "plugin_store_keys_refused", "code", code, "err", verr)
	}
	s.mu.Lock()
	k := s.keys
	s.mu.Unlock()
	if k == nil {
		if err == nil {
			err = errors.New("no store-keys.json verifies")
		}
		return nil, err
	}
	return k, nil
}

// FetchRevoked fetches revoked.json and applies it. A list older than the newest seen, or
// one that does not match the head that announced it, is refused and the current one kept.
func (s *Service) FetchRevoked(ctx context.Context, head *pluginsig.RevokedHead) error {
	st := s.State(ctx)
	keys, err := s.refreshKeys(ctx, &st)
	if err != nil {
		return err
	}
	raw, err := s.Store.Get(ctx, "revoked.json")
	if err != nil {
		return err
	}
	r, err := keys.VerifyRevoked(raw, st, s.now())
	if err != nil {
		code, _ := pluginsig.CodeOf(err)
		s.log().WarnContext(ctx, "plugins: revocation list refused", "event", code, "err", err)
		return err
	}
	if head != nil && !head.Matches(r) {
		s.log().WarnContext(ctx, "plugins: the revocation head names another list", "event", pluginsig.CodeRevocationHead,
			"head_version", head.Version, "list_version", r.Version)
		return &pluginsig.Error{Code: pluginsig.CodeRevocationHead, Step: pluginsig.StepList, Msg: "the head names another list"}
	}
	st.AcceptRevoked(r)
	if err := s.saveList(ctx, "revoked.json", raw, r.Version); err != nil {
		return err
	}
	if err := s.saveState(ctx, st); err != nil {
		return err
	}
	s.mu.Lock()
	s.revoked = r
	s.lastList = s.now()
	s.mu.Unlock()
	return s.Apply(ctx)
}

// PollHead fetches revoked-head.json; a newer version than the list the home has fetches
// the list.
func (s *Service) PollHead(ctx context.Context) error {
	s.mu.Lock()
	keys, have := s.keys, s.revoked
	s.lastHead = s.now()
	s.mu.Unlock()
	if keys == nil {
		return s.FetchRevoked(ctx, nil)
	}
	raw, err := s.Store.Get(ctx, "revoked-head.json")
	if err != nil {
		return err
	}
	h, err := keys.VerifyRevokedHead(raw)
	if err != nil {
		return err
	}
	st := s.State(ctx)
	st.AcceptRevokedHead(h)
	if err := s.saveState(ctx, st); err != nil {
		return err
	}
	if have != nil && h.Version <= have.Version {
		return nil
	}
	return s.FetchRevoked(ctx, h)
}

// FetchCatalog fetches catalog-<major>.json (anti-rollback, expiry as the lists).
func (s *Service) FetchCatalog(ctx context.Context) error {
	st := s.State(ctx)
	keys, err := s.refreshKeys(ctx, &st)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("catalog-%d.json", s.major())
	raw, err := s.Store.Get(ctx, name)
	if err != nil {
		return err
	}
	c, err := keys.VerifyCatalog(raw, s.major(), st, s.now())
	if err != nil {
		code, _ := pluginsig.CodeOf(err)
		s.log().WarnContext(ctx, "plugins: catalog refused", "event", code, "err", err)
		return err
	}
	st.AcceptCatalog(c)
	if err := s.saveList(ctx, name, raw, c.Version); err != nil {
		return err
	}
	if err := s.saveState(ctx, st); err != nil {
		return err
	}
	s.mu.Lock()
	s.catalog = c
	s.lastCat = s.now()
	s.mu.Unlock()
	return nil
}

// Active: the loop fetches only while a plugin is installed or an admin opened Browse in the
// last 7 days (54.17 §1).
func (s *Service) Active(ctx context.Context) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM plugin_installs`).Scan(&n)
	if n > 0 {
		return true
	}
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'plugins.browse_opened_at'`).Scan(&raw); err == nil {
		var at time.Time
		if json.Unmarshal(raw, &at) == nil && s.now().Sub(at) < ActiveFor {
			return true
		}
	}
	return false
}

// Tick is one pass of the loop: the head every 5 minutes, the list every hour, the catalog
// every 6 hours while plugins are installed.
func (s *Service) Tick(ctx context.Context) {
	if !s.Active(ctx) {
		return
	}
	s.mu.Lock()
	needList := s.now().Sub(s.lastList) >= ListEvery
	needCat := s.now().Sub(s.lastCat) >= CatalogEvery
	s.mu.Unlock()
	var err error
	if needList {
		err = s.FetchRevoked(ctx, nil)
	} else {
		err = s.PollHead(ctx)
	}
	if err == nil && needCat {
		err = s.FetchCatalog(ctx)
	}
	s.seen(ctx)
	s.mu.Lock()
	if err != nil {
		s.lastErr = err.Error()
	} else {
		s.lastErr = ""
	}
	s.mu.Unlock()
	if err != nil {
		s.log().WarnContext(ctx, "plugins: store fetch failed", "event", "plugin_catalog_fetch_failed", "err", err)
	}
}

// Run is the loop `api:plugins_catalog` (declared on Epic 51's runner).
func (s *Service) Run(ctx context.Context) {
	if err := s.Load(ctx); err != nil {
		s.log().WarnContext(ctx, "plugins: the kept lists could not be read", "event", "plugin_catalog_load_failed", "err", err)
	}
	for {
		s.Tick(ctx)
		wait := HeadEvery + time.Duration(rand.Int64N(int64(2*HeadJitter))) - HeadJitter
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		s.NightlyUpdates(ctx)
	}
}

// ---- the kill switch (54.17 §3) -------------------------------------------------------

// MatchVersions reports whether version is in a revocation entry's range: "*" every version,
// a plain version that version only, otherwise a range in the manifest's form.
func MatchVersions(rng, version string) bool {
	rng = strings.TrimSpace(rng)
	if rng == "*" || rng == "" {
		return true
	}
	if !strings.ContainsAny(rng, "<>=") {
		return strings.TrimPrefix(rng, "v") == strings.TrimPrefix(version, "v")
	}
	return components.VersionInRange(version, rng)
}

// entryFor is the strongest entry that covers an installed plugin (disable over warn over
// delist).
func entryFor(r *pluginsig.Revoked, id, version, devKey string) *pluginsig.RevocationEntry {
	if r == nil {
		return nil
	}
	rank := map[string]int{pluginsig.ActionDelist: 1, pluginsig.ActionWarn: 2, pluginsig.ActionDisable: 3}
	var best *pluginsig.RevocationEntry
	for i := range r.Entries {
		e := &r.Entries[i]
		if e.PluginID != id && e.PluginID != "*" {
			continue
		}
		if e.DeveloperKeyID != "" && e.DeveloperKeyID != devKey {
			continue
		}
		if !MatchVersions(e.Versions, version) {
			continue
		}
		if best == nil || rank[e.Action] > rank[best.Action] {
			best = e
		}
	}
	return best
}

// Revocation is what applies to an installed plugin.
type Revocation struct {
	Action     string `json:"action"`
	ReasonCode string `json:"reason_code"`
	Versions   string `json:"versions"`
	Issued     string `json:"issued"`
}

// Apply matches the current list against every installed plugin and acts (54.17 §3): a
// `disable` sets plugin_installs.revoked (the revoked gate) and an Epic 51 hold
// (store-revocation), with one notice to admins; a `warn` shows and notifies; a `delist`
// changes nothing installed. An entry gone from a newer list clears revoked; the hold stays
// until an admin starts the plugin, and a notice says it was cleared.
func (s *Service) Apply(ctx context.Context) error {
	s.mu.Lock()
	r := s.revoked
	s.mu.Unlock()
	rows, err := s.DB.QueryContext(ctx, `SELECT plugin_id, version, developer_key_id, revoked, revocation FROM plugin_installs`)
	if err != nil {
		return err
	}
	type inst struct {
		id, version, key string
		revoked          bool
		prev             *Revocation
	}
	var all []inst
	for rows.Next() {
		var i inst
		var raw []byte
		if err := rows.Scan(&i.id, &i.version, &i.key, &i.revoked, &raw); err != nil {
			rows.Close()
			return err
		}
		if len(raw) > 0 && string(raw) != "null" {
			var p Revocation
			if json.Unmarshal(raw, &p) == nil {
				i.prev = &p
			}
		}
		all = append(all, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, i := range all {
		e := entryFor(r, i.id, i.version, i.key)
		var now *Revocation
		if e != nil {
			now = &Revocation{Action: e.Action, ReasonCode: e.ReasonCode, Versions: e.Versions, Issued: e.Issued}
		}
		disable := now != nil && now.Action == pluginsig.ActionDisable
		same := (now == nil && i.prev == nil) || (now != nil && i.prev != nil && *now == *i.prev)
		if same && disable == i.revoked {
			continue
		}
		var raw any
		if now != nil {
			b, _ := json.Marshal(now)
			raw = string(b)
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE plugin_installs SET revoked = $2, revocation = $3 WHERE plugin_id = $1`, i.id, disable, raw); err != nil {
			return err
		}
		switch {
		case disable && !i.revoked:
			if s.Hold != nil {
				if err := s.Hold(ctx, i.id, true, HoldRevoked); err != nil {
					s.log().WarnContext(ctx, "plugins: hold for a revocation failed", "plugin", i.id, "err", err)
				}
			}
			s.notice(ctx, i.id, "notify.plugins.revoked", "error", now)
			s.log().WarnContext(ctx, "plugins: a store revocation disabled a plugin", "event", "plugin_revoked", "plugin", i.id,
				"version", i.version, "reason", now.ReasonCode)
		case now != nil && now.Action == pluginsig.ActionWarn && (i.prev == nil || *i.prev != *now):
			s.notice(ctx, i.id, "notify.plugins.revocation_warn", "warn", now)
		case now == nil && i.prev != nil && i.prev.Action != pluginsig.ActionDelist:
			s.notice(ctx, i.id, "notify.plugins.revocation_lifted", "info", i.prev)
			s.log().InfoContext(ctx, "plugins: a revocation was lifted", "event", "plugin_revocation_lifted", "plugin", i.id)
		}
	}
	return nil
}

func (s *Service) notice(ctx context.Context, id, key, severity string, r *Revocation) {
	params, _ := json.Marshal(map[string]any{"plugin": id, "reason": r.ReasonCode, "action": r.Action, "versions": r.Versions})
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO notifications (kind, severity, title_key, params, link, audience, subject)
		VALUES ('plugin', $1, $2, $3, '/settings/plugins', 'admins', $4)`, severity, key, string(params), id); err != nil {
		s.log().WarnContext(ctx, "plugins: notice failed", "plugin", id, "err", err)
	}
}

// Revoked reports whether a version of a plugin (with its developer key) is covered by a
// `disable` (an install, update or rollback to it is refused, 54.17 edge cases).
func (s *Service) Revoked(id, version, devKey string) *Revocation {
	s.mu.Lock()
	r := s.revoked
	s.mu.Unlock()
	if e := entryFor(r, id, version, devKey); e != nil && e.Action == pluginsig.ActionDisable {
		return &Revocation{Action: e.Action, ReasonCode: e.ReasonCode, Versions: e.Versions, Issued: e.Issued}
	}
	return nil
}

// TrustStatus is what the Plugins page shows about the lists.
type TrustStatus struct {
	RevokedVersion  int64      `json:"revoked_version"`
	RevokedIssued   *time.Time `json:"revoked_issued,omitempty"`
	RevokedExpired  bool       `json:"revoked_expired"`
	CatalogVersion  int64      `json:"catalog_version"`
	CatalogIssued   *time.Time `json:"catalog_issued,omitempty"`
	CatalogExpired  bool       `json:"catalog_expired"`
	StoreKeys       bool       `json:"store_keys"`
	LastError       string     `json:"last_error,omitempty"`
	StoreInstallsOK bool       `json:"store_installs_ok"`
}

func (s *Service) Trust(ctx context.Context) TrustStatus {
	st := s.State(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := TrustStatus{StoreKeys: s.keys != nil, LastError: s.lastErr}
	if s.revoked != nil {
		at := s.revoked.IssuedAt
		out.RevokedVersion, out.RevokedIssued, out.RevokedExpired = s.revoked.Version, &at, s.revoked.Expired(st, s.now())
	}
	if s.catalog != nil {
		at := s.catalog.IssuedAt
		out.CatalogVersion, out.CatalogIssued, out.CatalogExpired = s.catalog.Version, &at, s.catalog.Expired(st, s.now())
	}
	// installs and updates from the store wait for a fresh revocation list (54.14 §4)
	out.StoreInstallsOK = s.revoked != nil && !out.RevokedExpired && s.keys != nil
	return out
}

// Catalog is the kept catalog (nil: none yet).
func (s *Service) Catalog() *pluginsig.Catalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catalog
}

// CatalogAge is how old the kept catalog is (0: none).
func (s *Service) CatalogAge() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalog == nil || s.lastCat.IsZero() {
		return 0
	}
	return s.now().Sub(s.lastCat)
}
