package channels

// Where channel art may be read from and written to, and who sees its paths.
//
// A served image (GET /api/channels/{id}/logo, /art/{kind}, both public)
// must lie under one of the art roots: <DataDir>/channel-art (uploads) or a
// folder the latest lineup plan's settings name for art (dz_logo_dir — the
// logos and their loading-<logo> pictures —, dz_watermark_dir and the folder
// of dz_watermark_file). Anything else, a UNC path (\\host\share) included,
// is a 404, so a stray logo_path can never read another server file.
// Every file the upload/revert code writes or removes is checked to be
// inside <DataDir>/channel-art the same way.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// artRootsTTL is how long the plan's art folders are cached (the public
// logo route is hit once per channel by every guide load).
const artRootsTTL = 30 * time.Second

type artRootCache struct {
	mu    sync.Mutex
	at    time.Time
	roots []string
}

// isUNC reports a Windows network path (\\host\share, //host/share,
// \\?\ and \\.\ device paths). Such a path is never read or served.
func isUNC(p string) bool {
	p = strings.TrimSpace(p)
	return strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `//`) ||
		strings.HasPrefix(p, `\/`) || strings.HasPrefix(p, `/\`)
}

// usableRoot cleans an art root; "" when it is not an absolute local
// folder below a filesystem root (a bare "/" or "C:\" would allow all).
func usableRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || isUNC(root) || !filepath.IsAbs(root) {
		return ""
	}
	root = filepath.Clean(root)
	if filepath.Dir(root) == root {
		return ""
	}
	return root
}

// within returns p cleaned and rebuilt from root when it lies strictly
// inside root (no "..", no other volume), else ok=false.
func within(root, p string) (string, bool) {
	root = usableRoot(root)
	if root == "" || isUNC(p) {
		return "", false
	}
	rel, err := filepath.Rel(root, filepath.Clean(p))
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.Join(root, rel), true
}

// uploadRoot is <DataDir>/channel-art ("" without a data dir).
func (h *Handler) uploadRoot() string {
	if strings.TrimSpace(h.DataDir) == "" {
		return ""
	}
	return usableRoot(filepath.Join(h.DataDir, ArtDirName))
}

// planArtRoots reads the art folders from the latest plan's settings.
func (h *Handler) planArtRoots(ctx context.Context) []string {
	var raw []byte
	if err := h.DB.QueryRowContext(ctx,
		`SELECT plan FROM channel_plans ORDER BY id DESC LIMIT 1`).Scan(&raw); err != nil {
		return nil
	}
	var plan struct {
		Config map[string]any `json:"config"`
	}
	if json.Unmarshal(raw, &plan) != nil {
		return nil
	}
	var out []string
	for _, k := range []string{"dz_logo_dir", "dz_watermark_dir"} {
		if r := usableRoot(strOf(plan.Config[k])); r != "" {
			out = append(out, r)
		}
	}
	if f := strOf(plan.Config["dz_watermark_file"]); f != "" {
		if r := usableRoot(filepath.Dir(f)); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// artRoots are the folders art may be served from (see the file comment).
func (h *Handler) artRoots(ctx context.Context) []string {
	h.rootCache.mu.Lock()
	defer h.rootCache.mu.Unlock()
	now := time.Now()
	if h.rootCache.roots == nil || now.Sub(h.rootCache.at) > artRootsTTL {
		roots := []string{}
		if r := h.uploadRoot(); r != "" {
			roots = append(roots, r)
		}
		roots = append(roots, h.planArtRoots(ctx)...)
		h.rootCache.roots, h.rootCache.at = roots, now
	}
	return append([]string(nil), h.rootCache.roots...)
}

// servablePath is p (cleaned) when it lies inside an art root, also once
// symlinks are resolved; ok=false otherwise.
func (h *Handler) servablePath(ctx context.Context, p string) (string, bool) {
	if isUNC(p) {
		return "", false
	}
	for _, root := range h.artRoots(ctx) {
		safe, ok := within(root, p)
		if !ok {
			continue
		}
		realRoot, err1 := filepath.EvalSymlinks(root)
		realFile, err2 := filepath.EvalSymlinks(safe)
		if err1 != nil || err2 != nil {
			// a missing file: let the open report it
			return safe, true
		}
		if _, ok := within(realRoot, realFile); ok {
			return safe, true
		}
	}
	return "", false
}

// artLock serialises uploads and reverts of one channel's kind of art so
// two of them never interleave their file writes and removals.
func (h *Handler) artLock(channelID, kind string) func() {
	v, _ := h.artLocks.LoadOrStore(channelID+"/"+kind, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// redactPaths drops the server's filesystem paths from a channel shown to
// a non-admin: the art paths, a file logo_path (a URL logo stays: the web
// app falls back to it) and the look's watermark/loading paths. The
// servable URLs (logo_url, art.*.url) stay.
func redactPaths(p *principal.Principal, c *Channel) {
	if p != nil && p.IsAdmin {
		return
	}
	if c.LogoPath != nil && !isRemoteLogo(*c.LogoPath) {
		c.LogoPath = nil
	}
	if c.Art != nil {
		c.Art.Logo.Path, c.Art.Watermark.Path, c.Art.Loading.Path = "", "", ""
	}
	if len(c.ModeConfig) == 0 {
		return
	}
	mc := decodeMap(c.ModeConfig)
	look, ok := mc["look"].(map[string]any)
	if !ok {
		return
	}
	if lp := strOf(look["loading_path"]); lp != "" && !isRemoteLogo(lp) {
		delete(look, "loading_path")
	}
	if wm, ok := look["watermark"].(map[string]any); ok {
		if wp := strOf(wm["path"]); wp != "" && !isRemoteLogo(wp) {
			delete(wm, "path")
		}
	}
	if b, err := json.Marshal(mc); err == nil {
		c.ModeConfig = b
	}
}
