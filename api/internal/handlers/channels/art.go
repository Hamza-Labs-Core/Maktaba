package channels

// Channel art: the logo, the corner watermark and the loading screen.
//
//	POST   /api/channels/{id}/art/{kind}   multipart "file" (admin)
//	PATCH  /api/channels/{id}/art/watermark {position, width_pct, margin_pct} (admin)
//	DELETE /api/channels/{id}/art/{kind}   back to the plan's value (admin)
//	GET    /api/channels/{id}/art/{kind}   the image (public, like /logo)
//
// What the stream engine reads stays where it always was: channels.logo_path
// and channels.mode_config->'look' ({"watermark": {path, position, width_pct,
// margin_pct}, "loading_path"}). Those columns always hold the EFFECTIVE art.
//
// What a person set by hand is also recorded in channel_art_overrides (slot
// 0115), one row per (channel, kind). The row is how the channel keeps
// following the lineup plan: the plan writer (pipeline write_channels) leaves
// look/logo out of its "did a user edit this channel" digest and lays the
// override rows over the plan's values when it rewrites the channel, so an
// upload is never mistaken for a user edit and never lost on the next
// rewrite. Reverting deletes the row and puts the plan's value back.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Art kinds.
const (
	ArtLogo      = "logo"
	ArtWatermark = "watermark"
	ArtLoading   = "loading"
)

// ArtMaxBytes is the largest image an upload may carry.
const ArtMaxBytes = 5 << 20

// ArtDirName is the folder under the data dir the uploaded art lives in:
// <data_dir>/channel-art/<channel id>/<kind>-<sha256 short>.<ext>.
const ArtDirName = "channel-art"

// Watermark defaults (what a new watermark gets until someone picks).
const (
	DefaultWatermarkPosition  = "top-right"
	DefaultWatermarkWidthPct  = 12.0
	DefaultWatermarkMarginPct = 2.0
)

// WatermarkPositions is the closed corner vocabulary.
var WatermarkPositions = []string{"top-right", "top-left", "bottom-right", "bottom-left"}

const overridesTable = "channel_art_overrides"

func validArtKind(k string) bool { return k == ArtLogo || k == ArtWatermark || k == ArtLoading }

// ArtImage is one piece of a channel's art as the client sees it.
type ArtImage struct {
	// URL serves the effective image (GET /api/channels/{id}/art/{kind});
	// the ?v= changes whenever the image does, so caches never show an old one.
	URL  string `json:"url,omitempty"`
	Path string `json:"path,omitempty"`
	// Overridden: set by hand (upload or edit) rather than by the plan.
	Overridden bool `json:"overridden"`
}

// WatermarkArt adds the corner and size of the watermark.
type WatermarkArt struct {
	ArtImage
	Position  string  `json:"position"`
	WidthPct  float64 `json:"width_pct"`
	MarginPct float64 `json:"margin_pct"`
}

// ChannelArt is the art block of the channel DTO.
type ChannelArt struct {
	Logo      ArtImage     `json:"logo"`
	Watermark WatermarkArt `json:"watermark"`
	Loading   ArtImage     `json:"loading"`
	// Strip is the channel's own strip settings (mode_config.look.strip,
	// Story 27.11); null: the rules tv_strip defaults apply.
	Strip map[string]any `json:"strip"`
}

// WatermarkSettingsRequest is the PATCH /art/watermark body.
type WatermarkSettingsRequest struct {
	Position  *string  `json:"position,omitempty"`
	WidthPct  *float64 `json:"width_pct,omitempty"`
	MarginPct *float64 `json:"margin_pct,omitempty"`
}

// ArtURL is where kind's image of channel id is served, versioned by path.
func ArtURL(id, kind, path string) string {
	sum := sha256.Sum256([]byte(path))
	return "/api/channels/" + url.PathEscape(id) + "/art/" + kind + "?v=" + hex.EncodeToString(sum[:4])
}

// ─── reading the effective art off a channel row ─────────────────────

func decodeMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
		if m == nil {
			m = map[string]any{}
		}
	}
	return m
}

func lookOf(c Channel) map[string]any {
	if l, ok := decodeMap(c.ModeConfig)["look"].(map[string]any); ok {
		return l
	}
	return nil
}

func strOf(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func numOf(v any, def float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return def
}

// effectiveArtValue is kind's current value on the row, in the shape the
// override row's `base` stores: a string path (logo, loading) or the
// watermark object; nil when unset.
func effectiveArtValue(c Channel, kind string) any {
	switch kind {
	case ArtLogo:
		if c.LogoPath != nil && strings.TrimSpace(*c.LogoPath) != "" {
			return strings.TrimSpace(*c.LogoPath)
		}
		return nil
	case ArtWatermark:
		if wm, ok := lookOf(c)["watermark"].(map[string]any); ok {
			return wm
		}
		return nil
	default:
		if p := strOf(lookOf(c)["loading_path"]); p != "" {
			return p
		}
		return nil
	}
}

// artPath is the image path of kind on the row ("" when none).
func artPath(c Channel, kind string) string {
	switch v := effectiveArtValue(c, kind).(type) {
	case string:
		return v
	case map[string]any:
		return strOf(v["path"])
	}
	return ""
}

// withArt fills Channel.Art from the row (Overridden flags come later,
// from the override rows: see attachOverrides).
func withArt(c Channel) Channel {
	a := &ChannelArt{}
	img := func(kind string) ArtImage {
		p := artPath(c, kind)
		if p == "" {
			return ArtImage{}
		}
		return ArtImage{URL: ArtURL(c.ID, kind, p), Path: p}
	}
	a.Logo = img(ArtLogo)
	a.Loading = img(ArtLoading)
	a.Watermark = WatermarkArt{
		ArtImage:  img(ArtWatermark),
		Position:  DefaultWatermarkPosition,
		WidthPct:  DefaultWatermarkWidthPct,
		MarginPct: DefaultWatermarkMarginPct,
	}
	if wm, ok := effectiveArtValue(c, ArtWatermark).(map[string]any); ok {
		if p := strOf(wm["position"]); validPosition(p) {
			a.Watermark.Position = p
		}
		a.Watermark.WidthPct = numOf(wm["width_pct"], DefaultWatermarkWidthPct)
		a.Watermark.MarginPct = numOf(wm["margin_pct"], DefaultWatermarkMarginPct)
	}
	a.Strip = stripOf(c)
	c.Art = a
	return c
}

func validPosition(p string) bool {
	for _, v := range WatermarkPositions {
		if p == v {
			return true
		}
	}
	return false
}

// ─── override rows ───────────────────────────────────────────────────

type artOverride struct {
	kind     string
	path     sql.NullString
	settings map[string]any
	base     json.RawMessage
	exists   bool
}

func (h *Handler) loadOverride(ctx context.Context, q queryer, channelID, kind string) (artOverride, error) {
	o := artOverride{kind: kind}
	var settings, base []byte
	err := q.QueryRowContext(ctx, `SELECT path, settings, base FROM `+overridesTable+
		` WHERE channel_id = $1 AND kind = $2`, channelID, kind).Scan(&o.path, &settings, &base)
	if errors.Is(err, sql.ErrNoRows) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	o.exists = true
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &o.settings)
	}
	if len(base) > 0 {
		o.base = base
	}
	return o, nil
}

// overriddenKinds maps channel id → the kinds set by hand. A missing table
// (slot 0115 not applied) reads as no overrides: the DTO stays servable.
func (h *Handler) overriddenKinds(ctx context.Context, ids []string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if len(ids) == 0 {
		return out
	}
	q := `SELECT channel_id, kind FROM ` + overridesTable
	var args []any
	if len(ids) == 1 {
		q += ` WHERE channel_id = $1`
		args = append(args, ids[0])
	}
	rows, err := h.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind string
		if rows.Scan(&id, &kind) != nil {
			continue
		}
		if out[id] == nil {
			out[id] = map[string]bool{}
		}
		out[id][kind] = true
	}
	return out
}

func applyOverrideFlags(c *Channel, kinds map[string]bool) {
	if c.Art == nil {
		return
	}
	c.Art.Logo.Overridden = kinds[ArtLogo]
	c.Art.Watermark.Overridden = kinds[ArtWatermark]
	c.Art.Loading.Overridden = kinds[ArtLoading]
}

func (h *Handler) attachOverrides(ctx context.Context, c *Channel) {
	applyOverrideFlags(c, h.overriddenKinds(ctx, []string{c.ID})[c.ID])
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func jsonText(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

// upsertOverride records kind as set by hand. path / settings are only
// written when given (nil keeps the stored one); base is only written on
// the first override (the value it replaced, for a channel no plan owns).
func upsertOverride(ctx context.Context, q queryer, channelID, kind string, path *string,
	settings map[string]any, base any, now time.Time,
) error {
	var p any
	if path != nil {
		p = *path
	}
	var s any
	if settings != nil {
		s = jsonText(settings)
	}
	_, err := q.ExecContext(ctx, `INSERT INTO `+overridesTable+
		` (channel_id, kind, path, settings, base, updated_at) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (channel_id, kind) DO UPDATE SET
			path = COALESCE(EXCLUDED.path, `+overridesTable+`.path),
			settings = COALESCE(EXCLUDED.settings, `+overridesTable+`.settings),
			updated_at = EXCLUDED.updated_at`,
		channelID, kind, p, s, jsonText(base), now)
	return err
}

// ─── writing the effective art onto the channel row ──────────────────

// setLookKey sets (or with nil removes) one key of mode_config.look and
// returns the new mode_config.
func setLookKey(modeConfig json.RawMessage, key string, value any) (string, error) {
	mc := decodeMap(modeConfig)
	look, _ := mc["look"].(map[string]any)
	if look == nil {
		look = map[string]any{}
	}
	if value == nil {
		delete(look, key)
	} else {
		look[key] = value
	}
	if len(look) == 0 {
		delete(mc, "look")
	} else {
		mc["look"] = look
	}
	b, err := json.Marshal(mc)
	return string(b), err
}

// writeEffective puts value (a path string, a watermark object, or nil)
// as kind's effective art on channel c.
func writeEffective(ctx context.Context, q queryer, c Channel, kind string, value any, now time.Time) error {
	if kind == ArtLogo {
		var v any
		if s, ok := value.(string); ok && s != "" {
			v = s
		}
		_, err := q.ExecContext(ctx, `UPDATE channels SET logo_path = $1, updated_at = $2 WHERE id = $3`,
			v, now, c.ID)
		return err
	}
	key := "loading_path"
	if kind == ArtWatermark {
		key = "watermark"
	}
	if s, ok := value.(string); ok && s == "" {
		value = nil
	}
	mc, err := setLookKey(c.ModeConfig, key, value)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE channels SET mode_config = $1, updated_at = $2 WHERE id = $3`,
		mc, now, c.ID)
	return err
}

// watermarkWith is the current watermark object (or a new one with the
// defaults) with path and/or settings laid over it.
func watermarkWith(c Channel, path *string, settings map[string]any) map[string]any {
	wm := map[string]any{}
	if cur, ok := effectiveArtValue(c, ArtWatermark).(map[string]any); ok {
		for k, v := range cur {
			wm[k] = v
		}
	} else {
		wm["position"] = DefaultWatermarkPosition
		wm["width_pct"] = DefaultWatermarkWidthPct
		wm["margin_pct"] = DefaultWatermarkMarginPct
	}
	if path != nil {
		wm["path"] = *path
	}
	for k, v := range settings {
		wm[k] = v
	}
	return wm
}

// ─── the plan's value (what a revert goes back to) ───────────────────

// planArt returns what the latest lineup plan says for kind on channel id.
// owned is false when no plan wrote this channel (external_links has no
// maktaba_channel row for it), or the plan tables are absent.
func (h *Handler) planArt(ctx context.Context, channelID, kind string) (value any, owned bool) {
	var key string
	if err := h.DB.QueryRowContext(ctx, `SELECT subject_id FROM external_links
		WHERE target = 'maktaba_channel' AND subject_kind = 'catalog' AND external_id = $1`,
		channelID).Scan(&key); err != nil {
		return nil, false
	}
	var raw []byte
	if err := h.DB.QueryRowContext(ctx,
		`SELECT plan FROM channel_plans ORDER BY id DESC LIMIT 1`).Scan(&raw); err != nil {
		return nil, true
	}
	var plan struct {
		Channels []struct {
			Key     string `json:"key"`
			Maktaba struct {
				LogoPath   string         `json:"logo_path"`
				ModeConfig map[string]any `json:"mode_config"`
			} `json:"maktaba"`
		} `json:"channels"`
	}
	if json.Unmarshal(raw, &plan) != nil {
		return nil, true
	}
	for _, ch := range plan.Channels {
		if ch.Key != key {
			continue
		}
		m := ch.Maktaba
		look, _ := m.ModeConfig["look"].(map[string]any)
		switch kind {
		case ArtLogo:
			if p := strings.TrimSpace(m.LogoPath); p != "" {
				return p, true
			}
		case ArtWatermark:
			if wm, ok := look["watermark"].(map[string]any); ok {
				return wm, true
			}
		default:
			if p := strOf(look["loading_path"]); p != "" {
				return p, true
			}
		}
		return nil, true
	}
	return nil, true
}

// ─── files ───────────────────────────────────────────────────────────

// sniffImage names the image type of b by its magic bytes. svg is only
// recognised as such (allowed for the logo alone by the caller).
func sniffImage(b []byte) (ext, contentType string, ok bool) {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return ".png", "image/png", true
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return ".jpg", "image/jpeg", true
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return ".gif", "image/gif", true
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return ".webp", "image/webp", true
	case looksLikeSVG(b):
		return ".svg", "image/svg+xml", true
	}
	return "", "", false
}

// looksLikeSVG: UTF-8 text that opens like XML/SVG and has an <svg root
// element near the top.
func looksLikeSVG(b []byte) bool {
	head := b
	if len(head) > 4096 {
		head = head[:4096]
	}
	// a cut at 4096 may split one multi-byte character: drop up to 3 bytes
	for i := 0; i < 3 && len(head) > 0 && len(b) > len(head) && !utf8.Valid(head); i++ {
		head = head[:len(head)-1]
	}
	if !utf8.Valid(head) {
		return false
	}
	t := bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	t = bytes.TrimLeft(t, " \t\r\n")
	if !bytes.HasPrefix(t, []byte("<")) {
		return false
	}
	lower := bytes.ToLower(t)
	return bytes.Contains(lower, []byte("<svg"))
}

// artDir is the folder channel id's art is kept in ("" when the API has no
// data dir).
func (h *Handler) artDir(channelID string) string {
	root := h.uploadRoot()
	if root == "" || !safeID(channelID) {
		return ""
	}
	dir, ok := within(root, filepath.Join(root, channelID))
	if !ok {
		return ""
	}
	return dir
}

// removeArtFiles deletes kind's stored files of a channel except keep.
func removeArtFiles(dir, kind, keep string) {
	if dir == "" {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(dir, kind+"-*"))
	for _, m := range matches {
		if keep != "" && filepath.Clean(m) == filepath.Clean(keep) {
			continue
		}
		if safe, ok := within(dir, m); ok {
			_ = os.Remove(safe)
		}
	}
}

// safeID: a channel id is used as a folder name.
func safeID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\:`) &&
		filepath.Base(id) == id
}

// ─── handlers ────────────────────────────────────────────────────────

func (h *Handler) loadAdminChannel(w http.ResponseWriter, r *http.Request) (Channel, string, bool) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return Channel{}, "", false
	}
	kind := chi.URLParam(r, "kind")
	if !validArtKind(kind) {
		httperror.Write(w, r, httperror.NotFound("art kind "+kind+" (logo, watermark or loading)"))
		return Channel{}, "", false
	}
	id := chi.URLParam(r, "id")
	c, err := h.repo().get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("channel "+id))
			return Channel{}, "", false
		}
		httperror.Write(w, r, httperror.Internal("load channel"))
		return Channel{}, "", false
	}
	return c, kind, true
}

func (h *Handler) writeChannel(w http.ResponseWriter, r *http.Request, id string) {
	c, err := h.repo().get(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("reload channel"))
		return
	}
	h.attachOverrides(r.Context(), &c)
	common.WriteJSON(w, r, http.StatusOK, c)
}

func tooLarge() *httperror.Error {
	return &httperror.Error{
		Type:   httperror.TypeBodyTooLarge,
		Title:  "image too large",
		Status: http.StatusRequestEntityTooLarge,
		Detail: "an image must be at most " + strconv.Itoa(ArtMaxBytes>>20) + " MB",
	}
}

func unsupported(detail string) *httperror.Error {
	return &httperror.Error{
		Type:   httperror.TypeUnsupportedMediaType,
		Title:  "not a supported image",
		Status: http.StatusUnsupportedMediaType,
		Detail: detail,
	}
}

// readArtFile reads the multipart "file" part, at most ArtMaxBytes.
func readArtFile(w http.ResponseWriter, r *http.Request) ([]byte, *httperror.Error) {
	if r.ContentLength > ArtMaxBytes+(64<<10) {
		return nil, tooLarge()
	}
	r.Body = http.MaxBytesReader(w, r.Body, ArtMaxBytes+(64<<10))
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, httperror.BadRequest("expected a multipart/form-data body with a \"file\" field")
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil, httperror.BadRequest("missing the \"file\" field")
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, tooLarge()
			}
			return nil, httperror.BadRequest("bad multipart body")
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		b, err := io.ReadAll(io.LimitReader(part, ArtMaxBytes+1))
		_ = part.Close()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, tooLarge()
			}
			return nil, httperror.BadRequest("could not read the file")
		}
		if len(b) > ArtMaxBytes {
			return nil, tooLarge()
		}
		if len(b) == 0 {
			return nil, httperror.BadRequest("the file is empty")
		}
		return b, nil
	}
}

// ArtUpload serves POST /api/channels/{id}/art/{kind}.
func (h *Handler) ArtUpload(w http.ResponseWriter, r *http.Request) {
	c, kind, ok := h.loadAdminChannel(w, r)
	if !ok {
		return
	}
	dir := h.artDir(c.ID)
	if dir == "" {
		httperror.Write(w, r, httperror.Internal("channel art storage is not configured (MAKTABA_DATA_DIR)"))
		return
	}
	data, herr := readArtFile(w, r)
	if herr != nil {
		httperror.Write(w, r, herr)
		return
	}
	ext, _, ok := sniffImage(data)
	if !ok {
		httperror.Write(w, r, unsupported("the file is not a PNG, JPEG, WebP or GIF image"))
		return
	}
	if ext == ".svg" && kind != ArtLogo {
		httperror.Write(w, r, unsupported("SVG is only accepted for the logo"))
		return
	}

	sum := sha256.Sum256(data)
	dest, ok := within(dir, filepath.Join(dir, kind+"-"+hex.EncodeToString(sum[:])[:12]+ext))
	if !ok {
		httperror.Write(w, r, httperror.Internal("store image"))
		return
	}
	// one upload or revert of this channel's kind at a time: their file
	// writes and removals must not interleave
	unlock := h.artLock(c.ID, kind)
	defer unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // served images, world-readable is fine
		httperror.Write(w, r, httperror.Internal("create art folder"))
		return
	}
	// A re-upload of the same image lands on the file already in use: on
	// a failed save that file must stay.
	_, statErr := os.Lstat(dest)
	existed := statErr == nil
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		httperror.Write(w, r, httperror.Internal("store image"))
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		httperror.Write(w, r, httperror.Internal("store image"))
		return
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		_ = os.Remove(tmp.Name())
		httperror.Write(w, r, httperror.Internal("store image"))
		return
	}

	if err := h.setOverride(r.Context(), c, kind, &dest, nil); err != nil {
		if !existed {
			_ = os.Remove(dest)
		}
		httperror.Write(w, r, httperror.Internal("save channel art: "+err.Error()))
		return
	}
	removeArtFiles(dir, kind, dest)
	h.writeChannel(w, r, c.ID)
}

// setOverride records a hand-set path and/or watermark settings for kind
// and writes the resulting effective art onto the channel, in one tx.
func (h *Handler) setOverride(ctx context.Context, c Channel, kind string, path *string, settings map[string]any) error {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	o, err := h.loadOverride(ctx, tx, c.ID, kind)
	if err != nil {
		return err
	}
	var base any
	if !o.exists {
		base = effectiveArtValue(c, kind)
	}
	now := h.now()
	if err := upsertOverride(ctx, tx, c.ID, kind, path, settings, base, now); err != nil {
		return err
	}
	var value any
	switch kind {
	case ArtWatermark:
		value = watermarkWith(c, path, settings)
	default:
		if path != nil {
			value = *path
		}
	}
	if err := writeEffective(ctx, tx, c, kind, value, now); err != nil {
		return err
	}
	return tx.Commit()
}

// revertArt drops kind's override and puts the plan's value back (for a
// channel no plan owns: the value the first override replaced).
func (h *Handler) revertArt(ctx context.Context, c Channel, kind string) error {
	target, owned := h.planArt(ctx, c.ID, kind)
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	o, err := h.loadOverride(ctx, tx, c.ID, kind)
	if err != nil {
		return err
	}
	if !owned {
		target = nil
		if len(o.base) > 0 {
			var b any
			if json.Unmarshal(o.base, &b) == nil {
				target = b
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+overridesTable+` WHERE channel_id = $1 AND kind = $2`,
		c.ID, kind); err != nil {
		return err
	}
	if err := writeEffective(ctx, tx, c, kind, target, h.now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	keep := ""
	switch v := target.(type) {
	case string:
		keep = v
	case map[string]any:
		keep = strOf(v["path"])
	}
	removeArtFiles(h.artDir(c.ID), kind, keep)
	return nil
}

// ArtRevert serves DELETE /api/channels/{id}/art/{kind}.
func (h *Handler) ArtRevert(w http.ResponseWriter, r *http.Request) {
	c, kind, ok := h.loadAdminChannel(w, r)
	if !ok {
		return
	}
	unlock := h.artLock(c.ID, kind)
	defer unlock()
	if err := h.revertArt(r.Context(), c, kind); err != nil {
		httperror.Write(w, r, httperror.Internal("revert channel art: "+err.Error()))
		return
	}
	h.writeChannel(w, r, c.ID)
}

// ArtSettings serves PATCH /api/channels/{id}/art/watermark.
func (h *Handler) ArtSettings(w http.ResponseWriter, r *http.Request) {
	c, kind, ok := h.loadAdminChannel(w, r)
	if !ok {
		return
	}
	if kind != ArtWatermark {
		httperror.Write(w, r, httperror.NotFound("only the watermark has settings"))
		return
	}
	var req WatermarkSettingsRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	settings := map[string]any{}
	var fe []httperror.FieldError
	if req.Position != nil {
		if !validPosition(*req.Position) {
			fe = append(fe, httperror.FieldError{Field: "position", Message: "must be one of " + strings.Join(WatermarkPositions, ", ")})
		} else {
			settings["position"] = *req.Position
		}
	}
	if req.WidthPct != nil {
		if v := *req.WidthPct; math.IsNaN(v) || v < 1 || v > 50 {
			fe = append(fe, httperror.FieldError{Field: "width_pct", Message: "must be between 1 and 50"})
		} else {
			settings["width_pct"] = v
		}
	}
	if req.MarginPct != nil {
		if v := *req.MarginPct; math.IsNaN(v) || v < 0 || v > 25 {
			fe = append(fe, httperror.FieldError{Field: "margin_pct", Message: "must be between 0 and 25"})
		} else {
			settings["margin_pct"] = v
		}
	}
	if len(fe) > 0 {
		httperror.Write(w, r, httperror.Unprocessable(fe))
		return
	}
	if len(settings) == 0 {
		h.writeChannel(w, r, c.ID)
		return
	}
	// The stored settings are merged so a later PATCH of one field keeps the others.
	o, err := h.loadOverride(r.Context(), h.DB, c.ID, kind)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load channel art: "+err.Error()))
		return
	}
	merged := map[string]any{}
	for k, v := range o.settings {
		merged[k] = v
	}
	for k, v := range settings {
		merged[k] = v
	}
	if err := h.setOverride(r.Context(), c, kind, nil, merged); err != nil {
		httperror.Write(w, r, httperror.Internal("save watermark settings: "+err.Error()))
		return
	}
	h.writeChannel(w, r, c.ID)
}

// ArtGet serves GET /api/channels/{id}/art/{kind}: public like /logo.
func (h *Handler) ArtGet(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if !validArtKind(kind) {
		httperror.Write(w, r, httperror.NotFound("art kind "+kind))
		return
	}
	h.serveArt(w, r, kind)
}

// keepLook returns incoming mode_config with its "look" replaced by the
// stored one (removed when none is stored).
func keepLook(incoming, stored json.RawMessage) (string, error) {
	in := map[string]any{}
	if err := json.Unmarshal(incoming, &in); err != nil || in == nil {
		return "", errors.New("mode_config must be a JSON object")
	}
	if look, ok := decodeMap(stored)["look"]; ok {
		in["look"] = look
	} else {
		delete(in, "look")
	}
	b, err := json.Marshal(in)
	return string(b), err
}
