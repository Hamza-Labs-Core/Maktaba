package hdhr

// The guide's images, served by the streaming service itself at the URLs its
// XMLTV and M3U advertise (unauthenticated, like the guide):
//
//	/channel/{id}/logo                       the channel's logo
//	/tv/art/channel/{id}/video/{video}       a programme's image
//
// A programme's image is, best first: its series' poster (series.poster_path,
// else the series' first episode's poster: the series card the library shows),
// the video's own thumbnail (videos.poster_path, the pipeline's cache
// ~/.maktaba/cache/thumbnails/{content_hash}/poster.jpg …), and last the
// channel's logo. Only a video in the channel's live schedule within the guide's
// window has an image, so the route cannot be used to browse the library.
//
// Same safety as the API's logo route (api/internal/handlers/channels/
// artpaths.go): a file is served only when it lies inside an allowed root (a
// logo: <DataDir>/channel-art or the lineup plan's art folders; a programme
// image: those or the thumbnail cache), also once symlinks are resolved, has an
// image extension (no SVG) and is not a network (UNC) path. No URL the guide
// gives carries a token or a secret.
//
// On a cluster node (Remote set) the files come from the home through the
// signed /internal/tv/assets route (cluster.AssetHandler), cached by content.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// ArtDirName is the folder under the data dir where uploaded channel art lives
// (the API's channels.ArtDirName).
const ArtDirName = "channel-art"

// Asset kinds of the guide's images on the home's signed asset route
// (/internal/tv/assets/{channel}/{kind}).
const (
	AssetGuideLogo  = "guide-logo"
	AssetGuideVideo = "guide-video" // + "/{video}"
)

// DefaultImageMaxAge is the guide images' Cache-Control max-age when no rule
// gives one (rules tv.guide_image_max_age_s).
const DefaultImageMaxAge = 24 * time.Hour

// ArtVideo is a video whose thumbnail can stand for a programme.
type ArtVideo struct {
	ID          string
	ContentHash string
	PosterPath  string
}

// ProgrammeArt is what a programme's image is chosen from.
type ProgrammeArt struct {
	// OnAir: the video is in the channel's live schedule in the window asked.
	OnAir bool
	// SeriesPoster is the series' own poster_path ("" when none or not an episode).
	SeriesPoster string
	// SeriesCard is the series' first episode (zero when not an episode).
	SeriesCard ArtVideo
	// Video is the programme's own video.
	Video ArtVideo
}

// ArtFetcher fetches a channel's art from the home (cluster.AssetCache): the
// local copy of kind, whose source on the home is src ("" when none).
type ArtFetcher interface {
	Asset(ctx context.Context, channelID uuid.UUID, kind, src string) string
}

// ProgrammeArtURL is the image route of a programme.
func ProgrammeArtURL(base string, channelID, videoID uuid.UUID) string {
	return base + "/tv/art/channel/" + channelID.String() + "/video/" + videoID.String()
}

// ChannelLogoURL is the logo route of a channel.
func ChannelLogoURL(base string, channelID uuid.UUID) string {
	return base + "/channel/" + channelID.String() + "/logo"
}

// MountArt wires the image routes alone (a cluster node serves them too).
func (g *GuideHandler) MountArt(r chi.Router) {
	r.Get("/channel/{id}/logo", g.Logo)
	r.Get("/tv/art/channel/{id}/video/{video}", g.ProgrammeImage)
}

// imageTypes are the image extensions served (no SVG: it can carry script).
var imageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

func (g *GuideHandler) maxAge(ctx context.Context) time.Duration {
	if g.MaxAgeFunc != nil {
		if d := g.MaxAgeFunc(ctx); d >= 0 {
			return d
		}
	}
	return DefaultImageMaxAge
}

// window is the guide's programme window around now.
func (g *GuideHandler) window(ctx context.Context) (time.Time, time.Time) {
	back, ahead := g.Back, g.Ahead
	if g.AheadFunc != nil {
		if a := g.AheadFunc(ctx); a > 0 {
			ahead = a
		}
	}
	if back <= 0 {
		back = 2 * time.Hour
	}
	if ahead <= 0 {
		ahead = 7 * 24 * time.Hour
	}
	now := g.now()
	return now.Add(-back), now.Add(ahead)
}

// Logo serves a channel's logo: a URL logo_path is a redirect, a file one is
// served when it lies in an art folder.
func (g *GuideHandler) Logo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad channel id", http.StatusBadRequest)
		return
	}
	g.serveLogo(w, r, id)
}

func (g *GuideHandler) serveLogo(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	logo, err := g.Repo.ChannelLogo(r.Context(), id)
	if err != nil || strings.TrimSpace(logo) == "" {
		http.NotFound(w, r)
		return
	}
	if u, ok := remoteLogo(logo); ok {
		w.Header().Set("Cache-Control", cacheControl(g.maxAge(r.Context())))
		http.Redirect(w, r, u, http.StatusFound)
		return
	}
	var p string
	if g.Remote != nil {
		p = g.Remote.Asset(r.Context(), id, AssetGuideLogo, logo)
	} else {
		p = g.LogoFile(r.Context(), id)
	}
	g.serveImage(w, r, p)
}

// ProgrammeImage serves a programme's image (see the file comment).
func (g *GuideHandler) ProgrammeImage(w http.ResponseWriter, r *http.Request) {
	ch, err1 := uuid.Parse(chi.URLParam(r, "id"))
	vid, err2 := uuid.Parse(strings.TrimSuffix(chi.URLParam(r, "video"), ".jpg"))
	if err1 != nil || err2 != nil {
		http.NotFound(w, r)
		return
	}
	art, ok := g.programmeArt(r.Context(), ch, vid)
	if !ok || !art.OnAir {
		http.NotFound(w, r)
		return
	}
	var p string
	if g.Remote != nil {
		p = g.Remote.Asset(r.Context(), ch, AssetGuideVideo+"/"+vid.String(), vid.String())
	} else {
		p = g.pickProgrammeFile(r.Context(), art)
	}
	if p == "" {
		g.serveLogo(w, r, ch) // no image of its own: the channel's logo
		return
	}
	g.serveImage(w, r, p)
}

func (g *GuideHandler) programmeArt(ctx context.Context, ch, vid uuid.UUID) (ProgrammeArt, bool) {
	start, end := g.window(ctx)
	art, err := g.Repo.ProgrammeArt(ctx, ch, vid, start, end)
	if err != nil {
		return ProgrammeArt{}, false
	}
	return art, true
}

// LogoFile is the servable file of a channel's logo on this host ("" when it
// has none, it is a URL, or it is not inside an art folder).
func (g *GuideHandler) LogoFile(ctx context.Context, id uuid.UUID) string {
	logo, err := g.Repo.ChannelLogo(ctx, id)
	if err != nil {
		return ""
	}
	logo = strings.TrimSpace(logo)
	if logo == "" || strings.Contains(logo, "://") && !pathmap.IsCanonical(logo) {
		return ""
	}
	return g.servable(g.pathMap().Apply(logo), g.artRoots(ctx))
}

// ProgrammeFile is the servable image of the programme on this host: "" when
// the video is not on the channel's air or has no image of its own.
func (g *GuideHandler) ProgrammeFile(ctx context.Context, ch, vid uuid.UUID) string {
	art, ok := g.programmeArt(ctx, ch, vid)
	if !ok || !art.OnAir {
		return ""
	}
	return g.pickProgrammeFile(ctx, art)
}

func (g *GuideHandler) pickProgrammeFile(ctx context.Context, art ProgrammeArt) string {
	roots := g.artRoots(ctx)
	if t := usableRoot(g.thumbnailRoot()); t != "" {
		roots = append(roots, t)
	}
	pm := g.pathMap()
	var cands []string
	if art.SeriesPoster != "" {
		cands = append(cands, pm.Apply(art.SeriesPoster))
	}
	cands = append(cands, g.videoCandidates(art.SeriesCard)...)
	cands = append(cands, g.videoCandidates(art.Video)...)
	for _, c := range cands {
		if p := g.servable(c, roots); p != "" {
			return p
		}
	}
	return ""
}

// videoCandidates are the images that can stand for a video, best first (the
// API's videos.posterCandidates): its poster, the cache's poster and small copy
// by content hash, the copy the mediaintel import staged by id.
func (g *GuideHandler) videoCandidates(v ArtVideo) []string {
	if v.ID == "" {
		return nil
	}
	var out []string
	if v.PosterPath != "" {
		out = append(out, g.pathMap().Apply(v.PosterPath))
	}
	if root := g.thumbnailRoot(); root != "" {
		if h := v.ContentHash; h != "" && !strings.ContainsAny(h, `/\.`) {
			dir := filepath.Join(root, h)
			out = append(out, filepath.Join(dir, "poster.jpg"), filepath.Join(dir, "poster.webp"),
				filepath.Join(dir, "small.jpg"))
		}
		if _, err := uuid.Parse(v.ID); err == nil {
			out = append(out, filepath.Join(root, "imported", v.ID+".jpg"))
		}
	}
	return out
}

// servable is p (cleaned) when it is a non-empty regular image file inside one
// of roots, also once symlinks are resolved; "" otherwise.
func (g *GuideHandler) servable(p string, roots []string) string {
	p = strings.TrimSpace(p)
	if p == "" || isUNC(p) || !filepath.IsAbs(p) {
		return ""
	}
	if _, ok := imageTypes[strings.ToLower(filepath.Ext(p))]; !ok {
		return ""
	}
	for _, root := range roots {
		safe, ok := within(root, p)
		if !ok {
			continue
		}
		realRoot, err1 := filepath.EvalSymlinks(root)
		realFile, err2 := filepath.EvalSymlinks(safe)
		if err1 != nil || err2 != nil {
			continue
		}
		if _, ok := within(realRoot, realFile); !ok {
			continue
		}
		if fi, err := os.Stat(realFile); err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			continue
		}
		return safe
	}
	return ""
}

// serveImage serves p ("" or unreadable: 404) with the guide's cache headers.
func (g *GuideHandler) serveImage(w http.ResponseWriter, r *http.Request, p string) {
	if p == "" {
		http.NotFound(w, r)
		return
	}
	ct, ok := imageTypes[strings.ToLower(filepath.Ext(p))]
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p) //nolint:gosec // inside an allowed root (servable), or the node's asset cache
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", cacheControl(g.maxAge(r.Context())))
	w.Header().Set("ETag", `"`+strconv.FormatInt(fi.ModTime().UnixNano(), 36)+"-"+strconv.FormatInt(fi.Size(), 36)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func cacheControl(d time.Duration) string {
	if d <= 0 {
		return "no-cache"
	}
	return "public, max-age=" + strconv.Itoa(int(d/time.Second))
}

// remoteLogo is logo_path as an http(s) URL to redirect to.
func remoteLogo(logo string) (string, bool) {
	if !strings.Contains(logo, "://") || pathmap.IsCanonical(logo) {
		return "", false
	}
	u, err := url.Parse(logo)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.String(), true
}

func (g *GuideHandler) pathMap() pathmap.Map {
	if g.PathMapSource != nil {
		return g.PathMapSource()
	}
	return g.PathMap
}

// thumbnailRoot is the pipeline's thumbnail cache: ThumbnailRoot, else
// MAKTABA_THUMBNAIL_ROOT, else ~/.maktaba/cache/thumbnails.
func (g *GuideHandler) thumbnailRoot() string {
	if g.ThumbnailRoot != "" {
		return g.ThumbnailRoot
	}
	return ThumbnailRootFromEnv()
}

// ThumbnailRootFromEnv is MAKTABA_THUMBNAIL_ROOT, else ~/.maktaba/cache/thumbnails
// (the pipeline's default).
func ThumbnailRootFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("MAKTABA_THUMBNAIL_ROOT")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".maktaba", "cache", "thumbnails")
}

// DataDirFromEnv is MAKTABA_DATA_DIR, else /var/lib/maktaba (the API's).
func DataDirFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("MAKTABA_DATA_DIR")); v != "" {
		return v
	}
	return "/var/lib/maktaba"
}

// artRootsTTL is how long the plan's art folders are cached.
const artRootsTTL = 30 * time.Second

type artRootCache struct {
	mu    sync.Mutex
	at    time.Time
	roots []string
}

// artRoots are the folders a logo may be served from: <DataDir>/channel-art
// and the lineup plan's art folders.
func (g *GuideHandler) artRoots(ctx context.Context) []string {
	g.roots.mu.Lock()
	defer g.roots.mu.Unlock()
	now := time.Now()
	if g.roots.roots == nil || now.Sub(g.roots.at) > artRootsTTL {
		roots := []string{}
		if g.DataDir != "" {
			if r := usableRoot(filepath.Join(g.DataDir, ArtDirName)); r != "" {
				roots = append(roots, r)
			}
		}
		if plan, err := g.Repo.PlanArtRoots(ctx); err == nil {
			pm := g.pathMap()
			for _, p := range plan {
				if r := usableRoot(pm.Apply(p)); r != "" {
					roots = append(roots, r)
				}
			}
		}
		g.roots.roots, g.roots.at = roots, now
	}
	return append([]string(nil), g.roots.roots...)
}

// PlanArtFolders reads the art folders named by a lineup plan's settings:
// dz_logo_dir (logos and their loading pictures), dz_watermark_dir and the
// folder of dz_watermark_file.
func PlanArtFolders(plan []byte) []string {
	var p struct {
		Config map[string]any `json:"config"`
	}
	if json.Unmarshal(plan, &p) != nil {
		return nil
	}
	str := func(k string) string { s, _ := p.Config[k].(string); return strings.TrimSpace(s) }
	var out []string
	for _, k := range []string{"dz_logo_dir", "dz_watermark_dir"} {
		if s := str(k); s != "" {
			out = append(out, s)
		}
	}
	if f := str("dz_watermark_file"); f != "" {
		out = append(out, filepath.Dir(f))
	}
	return out
}

// isUNC reports a Windows network path (\\host\share, //host/share, \\?\ and
// \\.\ device paths). Such a path is never read or served.
func isUNC(p string) bool {
	p = strings.TrimSpace(p)
	return strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `//`) ||
		strings.HasPrefix(p, `\/`) || strings.HasPrefix(p, `/\`)
}

// usableRoot cleans an art root; "" when it is not an absolute local folder
// below a filesystem root (a bare "/" or "C:\" would allow all).
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

// within returns p cleaned and rebuilt from root when it lies strictly inside
// root (no "..", no other volume), else ok=false.
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
