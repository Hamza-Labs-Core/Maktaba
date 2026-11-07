package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginsig"
)

// Platform is this machine's ("darwin-arm64"), matched against a version's platforms.
type Platform string

func platformOK(platforms []string, p Platform) bool {
	return len(platforms) == 0 || slices.Contains(platforms, "any") || slices.Contains(platforms, string(p))
}

// Compatible reports whether a catalog version runs on this server (54.18 §1): its maktaba
// range includes the server version, its plugin API is served, and the platform fits.
func Compatible(v pluginsig.CatalogVersion, server string, p Platform) bool {
	return components.VersionInRange(server, v.Maktaba) && components.PluginAPIServed(v.PluginAPI) && platformOK(v.Platforms, p)
}

// newest is the newest version compatible with this server (nil: none).
func newest(vs []pluginsig.CatalogVersion, server string, p Platform) *pluginsig.CatalogVersion {
	var best *pluginsig.CatalogVersion
	for i := range vs {
		v := &vs[i]
		if !Compatible(*v, server, p) {
			continue
		}
		if best == nil || cmpVersion(v.Version, best.Version) > 0 {
			best = v
		}
	}
	return best
}

func cmpVersion(a, b string) int {
	if a == b {
		return 0
	}
	if components.VersionInRange(a, ">"+b) {
		return 1
	}
	return -1
}

// Entry is one plugin as Browse shows it.
type Entry struct {
	pluginsig.CatalogPlugin
	// Newest is the newest compatible version (nil: none, shown only when asked).
	Newest *pluginsig.CatalogVersion `json:"newest"`
	// Installed is the installed version ("" none); Update the newer compatible one.
	Installed  string `json:"installed,omitempty"`
	Update     string `json:"update,omitempty"`
	AutoUpdate bool   `json:"auto_update"`
	// Withdrawn: a revocation names it (warn or disable).
	Withdrawn *Revocation `json:"withdrawn,omitempty"`
}

// View is GET /api/plugins/catalog.
type View struct {
	Plugins []Entry     `json:"plugins"`
	Trust   TrustStatus `json:"trust"`
	// Opening: no catalog with listed plugins was fetched yet ("the store is opening soon").
	Opening bool `json:"opening"`
}

// Browse is the catalog filtered to what runs here (54.18 §1); with all, incompatible
// plugins too (each with no Newest). Opening it records that Browse was opened (the loop
// then fetches for 7 days) and fetches the catalog when it is older than an hour.
func (s *Service) Browse(ctx context.Context, server string, p Platform, all bool) (*View, error) {
	b, _ := json.Marshal(s.now().UTC())
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ('plugins.browse_opened_at', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, string(b))
	if c := s.Catalog(); c == nil || s.CatalogAge() > BrowseFresh {
		if err := s.FetchCatalog(ctx); err != nil {
			s.log().WarnContext(ctx, "plugins: catalog fetch on Browse failed", "event", "plugin_catalog_fetch_failed", "err", err)
		}
	}
	installed := map[string]struct {
		version string
		auto    bool
	}{}
	rows, err := s.DB.QueryContext(ctx, `SELECT plugin_id, version, auto_update FROM plugin_installs`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, v string
		var auto bool
		if err := rows.Scan(&id, &v, &auto); err != nil {
			rows.Close()
			return nil, err
		}
		installed[id] = struct {
			version string
			auto    bool
		}{v, auto}
	}
	rows.Close()
	view := &View{Trust: s.Trust(ctx), Plugins: []Entry{}}
	c := s.Catalog()
	view.Opening = c == nil || len(c.Plugins) == 0
	if c == nil {
		return view, nil
	}
	for _, cp := range c.Plugins {
		e := Entry{CatalogPlugin: cp, Newest: newest(cp.Versions, server, p)}
		if e.Newest == nil && !all {
			continue
		}
		if in, ok := installed[cp.ID]; ok {
			e.Installed, e.AutoUpdate = in.version, in.auto
			if e.Newest != nil && cmpVersion(e.Newest.Version, in.version) > 0 {
				e.Update = e.Newest.Version
			}
		}
		if e.Newest != nil {
			e.Withdrawn = s.Revoked(cp.ID, e.Newest.Version, "")
		}
		view.Plugins = append(view.Plugins, e)
	}
	return view, nil
}

// Installer is what installs a package (plugins.Lifecycle).
type Installer interface {
	Install(ctx context.Context, req plugins.InstallRequest, actor svc.Actor) (*plugins.InstallResult, error)
}

// Problem is a refusal of the store routes.
type Problem struct {
	Status int
	Code   string
	Msg    string
}

func (p *Problem) Error() string { return p.Code + ": " + p.Msg }

// InstallFromStore installs (or updates to) a catalog version (54.17 §2): refused while the
// revocation list is expired or missing, for a version a `disable` covers, or one not in
// the catalog; the package comes from the CDN by its sha256, is checked against it, and the
// supervisor then verifies its signatures as for any file.
func (s *Service) InstallFromStore(ctx context.Context, l Installer, id, version string, grants *plugins.InstallRequest, actor svc.Actor) (*plugins.InstallResult, error) {
	t := s.Trust(ctx)
	if !t.StoreInstallsOK {
		return nil, &Problem{Status: http.StatusConflict, Code: CodeListExpired, Msg: "installs from the store wait for a fresh list of withdrawn plugins"}
	}
	c := s.Catalog()
	if c == nil {
		return nil, &Problem{Status: http.StatusConflict, Code: CodeNotInCatalog, Msg: "no catalog yet"}
	}
	var v *pluginsig.CatalogVersion
	for _, p := range c.Plugins {
		if p.ID != id {
			continue
		}
		for i := range p.Versions {
			if p.Versions[i].Version == version {
				v = &p.Versions[i]
			}
		}
	}
	if v == nil {
		return nil, &Problem{Status: http.StatusConflict, Code: CodeDelisted, Msg: fmt.Sprintf("%s %s is not in the catalog", id, version)}
	}
	if r := s.Revoked(id, version, ""); r != nil {
		return nil, &Problem{Status: http.StatusConflict, Code: CodeRevoked, Msg: fmt.Sprintf("the plugin store withdrew %s %s (%s)", id, version, r.ReasonCode)}
	}
	if !strings.HasPrefix(v.URL, "https://") && !strings.HasPrefix(v.URL, "http://127.0.0.1") && !strings.HasPrefix(v.URL, "http://localhost") {
		return nil, &Problem{Status: http.StatusBadGateway, Code: CodeDownloadFailed, Msg: "not an https URL"}
	}
	body, err := s.Store.Get(ctx, v.SHA256+".mkplugin")
	if err != nil {
		return nil, &Problem{Status: http.StatusBadGateway, Code: CodeDownloadFailed, Msg: err.Error()}
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != v.SHA256 || int64(len(body)) != v.Size {
		// a file the CDN served wrong is never kept (54.17 T07)
		s.log().WarnContext(ctx, "plugins: a downloaded package does not match the catalog", "event", "plugin_download_digest", "plugin", id)
		return nil, &Problem{Status: http.StatusBadGateway, Code: "package_digest_mismatch", Msg: "the downloaded file does not match the catalog"}
	}
	req := plugins.InstallRequest{Package: bytes.NewReader(body)}
	if grants != nil {
		req.Grants, req.Libraries, req.AllLibraries, req.GrantsSet, req.Enable, req.ExpectID = grants.Grants, grants.Libraries, grants.AllLibraries, grants.GrantsSet, grants.Enable, grants.ExpectID
	}
	return l.Install(ctx, req, actor)
}

// SetAutoUpdate is PUT /api/plugins/{id}/auto-update.
func (s *Service) SetAutoUpdate(ctx context.Context, id string, on bool) error {
	r, err := s.DB.ExecContext(ctx, `UPDATE plugin_installs SET auto_update = $2 WHERE plugin_id = $1`, id, on)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return &Problem{Status: http.StatusNotFound, Code: "plugin_not_installed", Msg: id}
	}
	return nil
}

// NightWindow is when automatic updates apply: 02:00 to 05:00 server time.
func NightWindow(t time.Time) bool { h := t.Hour(); return h >= 2 && h < 5 }

// Auto is what NightlyUpdates needs from the API: the installer, the server version and
// platform, and whether a plugin is kept off.
type Auto struct {
	L        Installer
	Server   string
	Platform Platform
}

// SetAuto wires the nightly updates.
func (s *Service) SetAuto(a *Auto) { s.auto = a }

// NightlyUpdates applies the updates of plugins with auto-update on, in the night window,
// when the new version adds no capability; one that adds a capability waits for the admin
// ("Needs your approval"), and a kept-off plugin is never turned on by it (54.17 §2, §3).
func (s *Service) NightlyUpdates(ctx context.Context) {
	if s.auto == nil || !NightWindow(s.now()) {
		return
	}
	c := s.Catalog()
	if c == nil {
		return
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT plugin_id, version, manifest FROM plugin_installs WHERE auto_update AND NOT revoked`)
	if err != nil {
		return
	}
	type inst struct {
		id, version string
		manifest    []byte
	}
	var all []inst
	for rows.Next() {
		var i inst
		if rows.Scan(&i.id, &i.version, &i.manifest) == nil {
			all = append(all, i)
		}
	}
	rows.Close()
	for _, i := range all {
		var have struct {
			Capabilities struct {
				Required, Optional []string
			}
		}
		_ = json.Unmarshal(i.manifest, &have)
		for _, cp := range c.Plugins {
			if cp.ID != i.id {
				continue
			}
			v := newest(cp.Versions, s.auto.Server, s.auto.Platform)
			if v == nil || cmpVersion(v.Version, i.version) <= 0 {
				continue
			}
			adds := false
			for _, cap := range v.Capabilities {
				if !slices.Contains(have.Capabilities.Required, cap) && !slices.Contains(have.Capabilities.Optional, cap) {
					adds = true
				}
			}
			if adds {
				s.markNeedsApproval(ctx, i.id, v.Version)
				continue
			}
			if _, err := s.InstallFromStore(ctx, s.auto.L, i.id, v.Version, &plugins.InstallRequest{ExpectID: i.id}, svc.Actor{By: "auto-update", Source: "system"}); err != nil {
				s.log().WarnContext(ctx, "plugins: an automatic update failed", "event", "plugin_auto_update_failed", "plugin", i.id, "err", err)
				continue
			}
			s.log().InfoContext(ctx, "plugins: updated automatically", "event", "plugin_auto_updated", "plugin", i.id, "version", v.Version)
		}
	}
}

func (s *Service) markNeedsApproval(ctx context.Context, id, version string) {
	b, _ := json.Marshal(map[string]string{"version": version})
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		"plugins.needs_approval."+id, string(b))
}

// NeedsApproval is the version an automatic update waits on the admin for ("" none).
func (s *Service) NeedsApproval(ctx context.Context, id string) string {
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, "plugins.needs_approval."+id).Scan(&raw); err != nil {
		return ""
	}
	var v struct{ Version string }
	_ = json.Unmarshal(raw, &v)
	return v.Version
}
