package channels

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// logoMaxAge is how long browsers/Plex may cache a channel logo. Logos
// change rarely; an admin edit bumps updated_at, which is in the ETag.
const logoMaxAge = 3600

// logoTypes are the image extensions a logo file may have, mapped to
// their Content-Type. Anything else is refused so logo_path can never
// be used to read an arbitrary server file.
var logoTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

// LogoURL is the API path the logo of channel id is served from.
func LogoURL(id string) string { return "/api/channels/" + url.PathEscape(id) + "/logo" }

// withLogoURL fills Channel.LogoURL from LogoPath.
func withLogoURL(c Channel) Channel {
	if c.LogoPath != nil && strings.TrimSpace(*c.LogoPath) != "" {
		c.LogoURL = LogoURL(c.ID)
	} else {
		c.LogoURL = ""
	}
	return c
}

// isRemoteLogo reports whether logo_path is an http(s) URL (served by
// redirect) rather than a file on this server.
func isRemoteLogo(p string) bool {
	u, err := url.Parse(p)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// ValidateLogoPath accepts "" (clear), an http(s) URL, or an absolute
// path to an image file.
func ValidateLogoPath(p string) error {
	p = strings.TrimSpace(p)
	if p == "" || isRemoteLogo(p) {
		return nil
	}
	if !filepath.IsAbs(p) {
		return errors.New("must be an http(s) URL or an absolute path to an image")
	}
	if _, ok := logoTypes[strings.ToLower(filepath.Ext(p))]; !ok {
		return errors.New("must be a .png, .jpg, .jpeg, .gif, .webp or .svg image")
	}
	return nil
}

// Logo serves GET /api/channels/{id}/logo. It is deliberately public:
// a channel logo is not sensitive, and players that cannot carry the web
// session (Plex reading the XMLTV icon, <img> on another origin) need it.
// A file logo is streamed with cache headers (ETag/Last-Modified, so a
// revalidation is a 304); a URL logo is a redirect.
func (h *Handler) Logo(w http.ResponseWriter, r *http.Request) { h.serveArt(w, r, ArtLogo) }

// serveArt streams (or redirects to) the effective image of kind.
func (h *Handler) serveArt(w http.ResponseWriter, r *http.Request, kind string) {
	id := chi.URLParam(r, "id")
	c, err := h.repo().get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("channel "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load channel"))
		return
	}
	p := artPath(c, kind)
	if p == "" {
		httperror.Write(w, r, httperror.NotFound("channel has no "+kind))
		return
	}
	if isRemoteLogo(p) {
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(logoMaxAge))
		http.Redirect(w, r, p, http.StatusFound)
		return
	}
	if ValidateLogoPath(p) != nil {
		httperror.Write(w, r, httperror.NotFound(kind+" is not a servable image"))
		return
	}
	f, err := os.Open(p) //nolint:gosec // admin-set path, image extensions only (ValidateLogoPath)
	if err != nil {
		httperror.Write(w, r, httperror.NotFound(kind+" file missing"))
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		httperror.Write(w, r, httperror.NotFound(kind+" file missing"))
		return
	}
	w.Header().Set("Content-Type", logoTypes[strings.ToLower(filepath.Ext(p))])
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(logoMaxAge))
	w.Header().Set("ETag", `"`+strconv.FormatInt(st.ModTime().UnixNano(), 36)+"-"+
		strconv.FormatInt(st.Size(), 36)+`"`)
	if strings.EqualFold(filepath.Ext(p), ".svg") {
		// An SVG can carry script; never let it run as a document.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", st.ModTime(), f)
}
