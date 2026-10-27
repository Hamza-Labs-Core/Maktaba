// The home's identity for the apps (Epic 42 story 42.2): what a player or remote app
// needs to list a home it found (mDNS, a QR code, a typed address) and to know it again.
//
//	GET /api/discovery/home   public, per IP:
//	  {"id","name","version","urls":[LAN base URLs…, then the relay URL],
//	   "sign_in":{"password":true,"code":true}}
//
// No user data and no address beyond the home's own advertised ones: the same things
// the mDNS announcement says, plus the base URLs an app can use.
package discovery

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// HomeInfo is this home as the apps see it.
type HomeInfo struct {
	// ID is the home's instance id (instance_identity), the mDNS TXT `id`.
	ID string
	// Name is the home's display name, the mDNS TXT `name`.
	Name    string
	Version string
	// URLs are the home's base URLs: the LAN ones first, then the relay (public) one.
	URLs []string
}

// HomeFunc describes this home. r is the request asking (its own origin is the last
// resort when the home knows no address of its own).
type HomeFunc func(ctx context.Context, r *http.Request) HomeInfo

type homeSignIn struct {
	Password bool `json:"password"`
	Code     bool `json:"code"`
}

type homeResponse struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Version string     `json:"version"`
	URLs    []string   `json:"urls"`
	SignIn  homeSignIn `json:"sign_in"`
}

// MountHome attaches GET /api/discovery/home. perMin caps it per IP (0: the default,
// 60; negative: no limit, for tests).
func (h *Handler) MountHome(r chi.Router, perMin int) {
	var limit []func(http.Handler) http.Handler
	if perMin >= 0 {
		if perMin == 0 {
			perMin = 60
		}
		limit = append(limit, mw.PerIP(perMin))
	}
	r.With(limit...).Get("/api/discovery/home", h.DiscoveryHome)
}

// DiscoveryHome answers who this home is.
func (h *Handler) DiscoveryHome(w http.ResponseWriter, r *http.Request) {
	info := h.home(r)
	common.WriteJSON(w, r, http.StatusOK, homeResponse{
		ID:      info.ID,
		Name:    info.Name,
		Version: info.Version,
		URLs:    info.URLs,
		// a code needs tokens to hand out: without a minter only the password signs in
		SignIn: homeSignIn{Password: true, Code: h.Minter != nil},
	})
}

// home is HomeFunc's answer, its URLs never empty (the request's own origin then).
func (h *Handler) home(r *http.Request) HomeInfo {
	var info HomeInfo
	if h.Home != nil {
		info = h.Home(r.Context(), r)
	}
	info.URLs = dedupeURLs(info.URLs)
	if len(info.URLs) == 0 {
		if o := RequestOrigin(r); o != "" {
			info.URLs = []string{o}
		}
	}
	if info.URLs == nil {
		info.URLs = []string{}
	}
	return info
}

// RequestOrigin is the scheme and host the request reached this home at ("" when it
// says no host).
func RequestOrigin(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" || strings.ContainsAny(host, "/ \\@") {
		return ""
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https") {
		scheme = "https"
	}
	return scheme + "://" + host
}

// dedupeURLs keeps the well-formed http(s) URLs, without a trailing slash, each once,
// in order.
func dedupeURLs(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimRight(strings.TrimSpace(s), "/")
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
