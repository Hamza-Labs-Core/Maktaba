package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// Asset kinds (a channel's art a station encodes with).
const (
	AssetLogo      = "logo"
	AssetWatermark = "watermark"
	AssetLoading   = "loading"
)

// AssetsPath is the home's signed channel-art route.
const AssetsPath = "/internal/tv/assets/{channel}/{kind}"

// AssetLooker loads a channel's look (channel.Repo satisfies it).
type AssetLooker interface {
	Look(ctx context.Context, channelID uuid.UUID) (channel.Look, error)
}

// GuideArtFiles resolves the guide's images on the home (hdhr.GuideHandler):
// the servable file, "" when there is none (or the video is not on air).
type GuideArtFiles interface {
	LogoFile(ctx context.Context, channelID uuid.UUID) string
	ProgrammeFile(ctx context.Context, channelID, videoID uuid.UUID) string
}

// AssetHandler serves a channel's art from the home's disk to the nodes (signed,
// ETag = sha256 of the file).
type AssetHandler struct {
	Looks   AssetLooker
	PathMap pathmap.Map
	Keyring *Keyring
	// Guide, when set, also serves the guide's images a node's guide image
	// routes show: kind "guide-logo" and "guide-video/{video}"
	// (hdhr.AssetGuideLogo, hdhr.AssetGuideVideo), with the same checks as
	// the home's public routes.
	Guide GuideArtFiles

	mu   sync.Mutex
	sums map[string]assetSum // path → hash at (size, mtime)
}

type assetSum struct {
	size int64
	mod  time.Time
	sha  string
}

// Mount wires the route behind the signature check.
func (h *AssetHandler) Mount(r chi.Router) {
	r.With(VerifyMiddleware(h.Keyring, nil)).Get(AssetsPath, h.serve)
	r.With(VerifyMiddleware(h.Keyring, nil)).Get(GuideVideoAssetPath, h.serveGuideVideo)
}

// The guide's images on the signed route (hdhr.AssetGuideLogo, hdhr.AssetGuideVideo).
const (
	assetGuideLogo = "guide-logo"
	// GuideVideoAssetPath is a programme's image.
	GuideVideoAssetPath = "/internal/tv/assets/{channel}/guide-video/{video}"
)

func (h *AssetHandler) serveGuideVideo(w http.ResponseWriter, r *http.Request) {
	ch, err1 := uuid.Parse(chi.URLParam(r, "channel"))
	vid, err2 := uuid.Parse(chi.URLParam(r, "video"))
	if err1 != nil || err2 != nil || h.Guide == nil {
		http.NotFound(w, r)
		return
	}
	h.serveFile(w, r, h.Guide.ProgrammeFile(r.Context(), ch, vid))
}

// assetPath picks the look's path for kind.
func assetPath(l channel.Look, kind string) string {
	switch kind {
	case AssetLogo:
		return l.LogoPath
	case AssetWatermark:
		if l.Watermark != nil {
			return l.Watermark.Path
		}
	case AssetLoading:
		return l.LoadingPath
	}
	return ""
}

func (h *AssetHandler) serve(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "channel"))
	if err != nil {
		http.Error(w, "bad channel", http.StatusBadRequest)
		return
	}
	if chi.URLParam(r, "kind") == assetGuideLogo {
		if h.Guide == nil {
			http.NotFound(w, r)
			return
		}
		h.serveFile(w, r, h.Guide.LogoFile(r.Context(), id))
		return
	}
	look, err := h.Looks.Look(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p := assetPath(look, chi.URLParam(r, "kind"))
	if p == "" || strings.Contains(p, "://") {
		http.NotFound(w, r)
		return
	}
	h.serveFile(w, r, h.PathMap.Apply(p))
}

// serveFile serves full ("" → 404) with its content hash as the ETag.
func (h *AssetHandler) serveFile(w http.ResponseWriter, r *http.Request, full string) {
	if full == "" {
		http.NotFound(w, r)
		return
	}
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	sum, err := h.hash(full, fi)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	etag := `"` + sum + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Maktaba-Asset-Ext", strings.ToLower(filepath.Ext(full)))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, filepath.Base(full), fi.ModTime(), f)
}

func (h *AssetHandler) hash(full string, fi os.FileInfo) (string, error) {
	h.mu.Lock()
	if s, ok := h.sums[full]; ok && s.size == fi.Size() && s.mod.Equal(fi.ModTime()) {
		h.mu.Unlock()
		return s.sha, nil
	}
	h.mu.Unlock()
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()
	d := sha256.New()
	if _, err := io.Copy(d, f); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(d.Sum(nil))
	h.mu.Lock()
	if h.sums == nil {
		h.sums = map[string]assetSum{}
	}
	h.sums[full] = assetSum{size: fi.Size(), mod: fi.ModTime(), sha: sum}
	h.mu.Unlock()
	return sum, nil
}

// AssetCache is a node's copy of the home's channel art, by content hash (AC7): a
// station asks for (channel, kind); the file is fetched from the home once, and asked
// again (If-None-Match) at most every TTL, so an art change on the home is picked up
// within the station's look TTL.
type AssetCache struct {
	// Base is the home's streaming base URL ("" while unknown).
	Base    func() string
	Keyring *Keyring
	Dir     string
	Client  *http.Client
	TTL     time.Duration
	Now     func() time.Time

	mu      sync.Mutex
	entries map[string]*cachedAsset
}

type cachedAsset struct {
	src     string
	sha     string
	path    string
	checked time.Time
}

// AssetTTL is how long a fetched asset is used before the home is asked again.
const AssetTTL = time.Minute

func (c *AssetCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Asset returns the local file of the channel's art kind (src is the path the look
// names on the home), or "" when the home has none.
func (c *AssetCache) Asset(ctx context.Context, channelID uuid.UUID, kind, src string) string {
	if src == "" {
		return ""
	}
	key := channelID.String() + "/" + kind
	ttl := c.TTL
	if ttl <= 0 {
		ttl = AssetTTL
	}
	c.mu.Lock()
	e := c.entries[key]
	if e != nil && e.src == src && c.now().Sub(e.checked) < ttl {
		p := e.path
		c.mu.Unlock()
		return p
	}
	c.mu.Unlock()
	path, sha, err := c.fetch(ctx, channelID, kind, e)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*cachedAsset{}
	}
	if err != nil {
		if e != nil {
			return e.path // keep the last copy while the home is unreachable
		}
		return ""
	}
	if path == "" {
		delete(c.entries, key)
		return ""
	}
	c.entries[key] = &cachedAsset{src: src, sha: sha, path: path, checked: c.now()}
	return path
}

// fetchError is an asset fetch failure.
type fetchError string

func (e fetchError) Error() string { return string(e) }

func (c *AssetCache) fetch(ctx context.Context, channelID uuid.UUID, kind string, prev *cachedAsset) (string, string, error) {
	base := ""
	if c.Base != nil {
		base = strings.TrimRight(c.Base(), "/")
	}
	if base == "" {
		return "", "", fetchError("cluster: home address unknown")
	}
	urlPath := "/internal/tv/assets/" + channelID.String() + "/" + kind
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+urlPath, nil)
	if err != nil {
		return "", "", err
	}
	c.Keyring.SignRequest(req, 0)
	if prev != nil && prev.sha != "" {
		req.Header.Set("If-None-Match", `"`+prev.sha+`"`)
	}
	cl := c.Client
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		if prev == nil {
			return "", "", fetchError("cluster: 304 without a copy")
		}
		return prev.path, prev.sha, nil
	case http.StatusNotFound:
		return "", "", nil
	case http.StatusOK:
	default:
		return "", "", fetchError("cluster: asset fetch: " + resp.Status)
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(c.Dir, ".asset-*")
	if err != nil {
		return "", "", err
	}
	d := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, d), io.LimitReader(resp.Body, 64<<20))
	cerr := tmp.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", "", err
	}
	sha := hex.EncodeToString(d.Sum(nil))
	ext := resp.Header.Get("X-Maktaba-Asset-Ext")
	if ext == "" || strings.ContainsAny(ext, `/\`) || len(ext) > 8 {
		ext = ""
	}
	final := filepath.Join(c.Dir, sha+ext)
	if _, err := os.Stat(final); err == nil {
		_ = os.Remove(tmp.Name())
	} else if err := os.Rename(tmp.Name(), final); err != nil {
		_ = os.Remove(tmp.Name())
		return "", "", err
	}
	return final, sha, nil
}
