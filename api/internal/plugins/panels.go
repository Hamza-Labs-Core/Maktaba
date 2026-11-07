package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginpanel"
)

// Panels (story 54.20): a plugin declares a panel as data (ui/panel.json, ui@1) and every
// app draws it natively. GET /api/panels answers the panels of a video, series, season or
// library for the signed-in user, everyone who can see the subject, viewers included,
// while the plugin is on; the values are the plugin's data for the subject, plain text
// checked when the plugin wrote them.

// CodeTextForbidden: a value a panel shows holds a URL, HTML, markdown or a control
// character (54.20 §2).
const (
	CodeTextForbidden = "plugin_text_forbidden"
)

// MaxPanelsPerPage is the most panels one page shows (54.20 §1).
const MaxPanelsPerPage = 3

// InstalledPanel is one [[panel]] of an installed plugin, parsed.
type InstalledPanel struct {
	On    string
	Panel *pluginpanel.Panel
}

type panelCacheEntry struct {
	digest string
	panels []InstalledPanel
	// the package's words, by language
	words map[string]map[string]string
}

var panelCache sync.Map // plugin id -> panelCacheEntry

// panelsOf reads an installed plugin's panels (cached by its package digest). A panel file
// that does not parse is left out.
func panelsOf(in *installRow) []InstalledPanel {
	return panelEntry(in).panels
}

func panelEntry(in *installRow) panelCacheEntry {
	if e, ok := panelCache.Load(in.ID); ok && e.(panelCacheEntry).digest == in.Digest {
		return e.(panelCacheEntry)
	}
	var m struct {
		Panel []struct {
			On     string `json:"on"`
			Schema string `json:"schema"`
		} `json:"panel"`
	}
	_ = json.Unmarshal(in.Manifest, &m)
	var out []InstalledPanel
	for _, p := range m.Panel {
		raw, err := os.ReadFile(filepath.Join(in.Path, filepath.FromSlash(p.Schema)))
		if err != nil {
			continue
		}
		if parsed, err := pluginpanel.Parse(raw); err == nil {
			out = append(out, InstalledPanel{On: p.On, Panel: parsed})
		}
	}
	e := panelCacheEntry{digest: in.Digest, panels: out, words: pluginWords(in.Path)}
	panelCache.Store(in.ID, e)
	return e
}

// CheckPanels is the install-time check of a package's panels (manifest_bad_panel).
func CheckPanels(dir string, manifest json.RawMessage) error {
	var m struct {
		Panel []struct {
			Schema string `json:"schema"`
		} `json:"panel"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return err
	}
	for _, p := range m.Panel {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p.Schema)))
		if err != nil {
			return &pluginpanel.Error{Code: "manifest_bad_panel", Detail: p.Schema + ": not in the package"}
		}
		if _, err := pluginpanel.Parse(raw); err != nil {
			return err
		}
	}
	return nil
}

// checkPanelValue refuses a value for a key a panel of this subject kind shows when it is
// not plain text or not the block's shape (the data/put route, 54.20 §2).
func (c *call) checkPanelValue(kind, key, value string) *Refusal {
	in, err := loadInstall(c.ctx, c.s.DB, c.inv.PluginID, false)
	if err != nil || in == nil {
		return nil
	}
	for _, p := range panelsOf(in) {
		if p.On != kind {
			continue
		}
		if b := p.Panel.Uses(key); b != nil {
			if e := pluginpanel.CheckValue(b, []byte(value)); e != nil {
				status := 400
				if e.Code == CodeTooLarge {
					status = 413
				}
				return refuse(status, e.Code, e.Detail)
			}
		}
	}
	return nil
}

// Panels serves GET /api/panels and GET /api/panels/image.
type Panels struct {
	L *Lifecycle
	// On answers whether a plugin shows its panels (nil: pluginOn, the components service).
	On func(ctx context.Context, id string) bool
}

func (h *Panels) on(ctx context.Context, id string) bool {
	if h.On != nil {
		return h.On(ctx, id)
	}
	return h.pluginOn(ctx, id)
}

// Mount adds the routes; any signed-in user may call them (54.20 §3).
func (h *Panels) Mount(r chi.Router) {
	r.Get("/api/panels", h.list)
	r.Get("/api/panels/image", h.image)
}

// PanelBlock is one block as the apps draw it: its type, its label in the user's
// language and its value (the plugin's data, already checked).
type PanelBlock struct {
	Type    string          `json:"type"`
	Label   string          `json:"label,omitempty"`
	Columns []string        `json:"columns,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
	// Image is the home's URL of the panel image (image blocks).
	Image string `json:"image,omitempty"`
}

// PanelView is one plugin's panel for a subject.
type PanelView struct {
	PluginID string       `json:"plugin_id"`
	Plugin   string       `json:"plugin"`
	Title    string       `json:"title"`
	Blocks   []PanelBlock `json:"blocks"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func panelError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"code": code})
}

// subjectLibrary answers the library a subject is in ("" for none: 404), as the subject's
// own routes read it.
func subjectLibrary(ctx context.Context, db *sql.DB, kind, id string) (string, error) {
	if !uuidRe.MatchString(id) {
		return "", nil
	}
	var q string
	switch kind {
	case "video":
		q = `SELECT CAST(library_id AS text) FROM videos WHERE id = $1 AND deleted_at IS NULL`
	case "series":
		q = `SELECT CAST(library_id AS text) FROM series WHERE id = $1`
	case "season":
		q = `SELECT CAST(library_id AS text) FROM series_seasons WHERE id = $1`
	case "library":
		q = `SELECT CAST(id AS text) FROM libraries WHERE id = $1`
	default:
		return "", nil
	}
	var lib string
	err := db.QueryRowContext(ctx, q, id).Scan(&lib)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return lib, err
}

// lang is the user's language for plugin words: ?lang=, else Accept-Language, else en.
func lang(r *http.Request) string {
	l := r.URL.Query().Get("lang")
	if l == "" {
		l = r.Header.Get("Accept-Language")
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "ar") {
		return "ar"
	}
	return "en"
}

// pluginOn: an enabled plugin that passes its gates (not off, kept off, held, waiting or
// revoked) shows its panels (54.20 §3).
func (h *Panels) pluginOn(ctx context.Context, id string) bool {
	if h.L == nil || h.L.Components == nil {
		return false
	}
	if h.L.keptOff(ctx, id) != nil {
		return false
	}
	d, err := h.L.Components.Show(ctx, components.PluginPrefix+id)
	if err != nil {
		return false
	}
	return d.Hold == nil && (d.Phase == components.PhaseRunning || d.Phase == components.PhaseDegraded)
}

func (h *Panels) subject(w http.ResponseWriter, r *http.Request) (kind, id string, ok bool) {
	p := principal.FromContext(r.Context())
	if p == nil {
		panelError(w, http.StatusUnauthorized, "auth_required")
		return "", "", false
	}
	kind, id, _ = strings.Cut(r.URL.Query().Get("subject"), ":")
	lib, err := subjectLibrary(r.Context(), h.L.DB, kind, id)
	if err != nil {
		panelError(w, http.StatusServiceUnavailable, "unavailable")
		return "", "", false
	}
	// a subject the user cannot see is 404, as its own page is
	if lib == "" || (!p.AccessAllLibraries && !p.HasLibrary(lib)) {
		panelError(w, http.StatusNotFound, "not_found")
		return "", "", false
	}
	return kind, id, true
}

func (h *Panels) list(w http.ResponseWriter, r *http.Request) {
	kind, id, ok := h.subject(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	language := lang(r)
	rows, err := h.L.DB.QueryContext(ctx, `SELECT plugin_id FROM plugin_installs ORDER BY installed_at, plugin_id`)
	if err != nil {
		panelError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	var ids []string
	for rows.Next() {
		var pid string
		if rows.Scan(&pid) == nil {
			ids = append(ids, pid)
		}
	}
	_ = rows.Close()
	// the admin's order (54.20 §1), then install order
	if order, err := LoadPanelOrder(ctx, h.L.DB); err == nil {
		ids = orderBy(ids, order)
	}
	var fileChanged *time.Time
	if kind == "video" {
		var mt sql.NullTime
		if h.L.DB.QueryRowContext(ctx, `SELECT mtime FROM videos WHERE id = $1`, id).Scan(&mt) == nil && mt.Valid {
			fileChanged = &mt.Time
		}
	}
	out := []PanelView{}
	for _, pid := range ids {
		if len(out) == MaxPanelsPerPage {
			break
		}
		in, err := loadInstall(ctx, h.L.DB, pid, false)
		if err != nil || in == nil {
			continue
		}
		var panel *pluginpanel.Panel
		for _, p := range panelsOf(in) {
			if p.On == kind {
				panel = p.Panel
				break
			}
		}
		if panel == nil || !h.on(ctx, pid) {
			continue
		}
		if v := h.view(ctx, in, panel, kind, id, language, fileChanged); v != nil {
			out = append(out, *v)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"panels": out})
}

// view is one plugin's panel with its values; nil when none of its keys has data for the
// subject (54.20 edge cases: the panel is not shown).
func (h *Panels) view(ctx context.Context, in *installRow, panel *pluginpanel.Panel, kind, id, language string,
	fileChanged *time.Time) *PanelView {
	words := panelEntry(in).words
	word := func(k string) string {
		if k == "" {
			return ""
		}
		if s := words[language][k]; s != "" && pluginpanel.Forbidden(s) == "" {
			return s
		}
		if s := words["en"][k]; s != "" && pluginpanel.Forbidden(s) == "" {
			return s
		}
		return ""
	}
	type datum struct {
		value   []byte
		updated time.Time
	}
	values := map[string]datum{}
	rows, err := h.L.DB.QueryContext(ctx, `SELECT key, value, updated_at FROM plugin_data
		WHERE plugin_id = $1 AND subject_kind = $2 AND subject_id = $3`, in.ID, kind, id)
	if err != nil {
		return nil
	}
	for rows.Next() {
		var k string
		var d datum
		if rows.Scan(&k, &d.value, &d.updated) == nil {
			values[k] = d
		}
	}
	_ = rows.Close()
	v := &PanelView{PluginID: in.ID, Plugin: word(pluginName(in)), Title: word(panel.Title)}
	if v.Plugin == "" {
		v.Plugin = in.ID
	}
	shown := 0
	for _, b := range panel.Blocks {
		pb := PanelBlock{Type: b.Type, Label: word(b.Label)}
		if b.Type == pluginpanel.Image {
			if panelImage(h.L.ArtifactsDir, in, kind, id) == "" {
				continue
			}
			pb.Image = "/api/panels/image?subject=" + kind + ":" + id + "&plugin=" + in.ID
			v.Blocks = append(v.Blocks, pb)
			continue
		}
		d, ok := values[b.Key]
		if !ok {
			continue
		}
		// markers belong to the file they were found in: written before the file last
		// changed, they are not shown
		if b.Type == pluginpanel.Markers && fileChanged != nil && d.updated.Before(*fileChanged) {
			continue
		}
		// checked when written; a value that is not the block's shape now (a newer panel
		// file) is left out
		if pluginpanel.CheckValue(&b, d.value) != nil {
			continue
		}
		for _, c := range b.Columns {
			pb.Columns = append(pb.Columns, word(c))
		}
		pb.Value = d.value
		v.Blocks = append(v.Blocks, pb)
		shown++
	}
	if shown == 0 && len(v.Blocks) == 0 {
		return nil
	}
	return v
}

func pluginName(in *installRow) string {
	var m struct {
		Strings struct {
			Name string `json:"name"`
		} `json:"strings"`
	}
	_ = json.Unmarshal(in.Manifest, &m)
	return m.Strings.Name
}

// panelImage is the path of a plugin's panel image for a subject ("" for none).
func panelImage(root string, in *installRow, kind, id string) string {
	if kind != "video" || root == "" {
		return ""
	}
	dir := filepath.Join(root, in.ID, id)
	for _, name := range []string{"panel.png", "panel.jpg"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.Size() <= pluginpanel.MaxImageBytes {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

func (h *Panels) image(w http.ResponseWriter, r *http.Request) {
	kind, id, ok := h.subject(w, r)
	if !ok {
		return
	}
	pid := r.URL.Query().Get("plugin")
	in, err := loadInstall(r.Context(), h.L.DB, pid, false)
	if err != nil || in == nil || !h.on(r.Context(), pid) {
		panelError(w, http.StatusNotFound, "not_found")
		return
	}
	shows := false
	for _, p := range panelsOf(in) {
		for _, b := range p.Panel.Blocks {
			if p.On == kind && b.Type == pluginpanel.Image {
				shows = true
			}
		}
	}
	path := panelImage(h.L.ArtifactsDir, in, kind, id)
	if !shows || path == "" {
		panelError(w, http.StatusNotFound, "not_found")
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		panelError(w, http.StatusNotFound, "not_found")
		return
	}
	ct := "image/png"
	if strings.HasSuffix(path, ".jpg") {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(b)
}
