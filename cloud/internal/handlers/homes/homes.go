// Package homes is a cloud account's view of the homes it reaches
// (Epic 42 story 42.7, linked accounts):
//
//	GET  /v1/homes                 — the homes this account is linked to or owns
//	POST /v1/homes/{id}/assertion  — a one-use home assertion for one of them
//	GET  /v1/homes/assertion-key   — the cloud's assertion public key (public)
//	GET  /v1/homes/{id}/link       — sends a browser linking on a home to the cloud web's
//	                                 confirm page (public: a redirect to this cloud only)
//
// An assertion names the cloud account and the home, for one use, a
// minute; it never says what the account may do there. The home
// verifies it, finds the home user it linked, and issues its own
// tokens with that user's role and the link's scope.
package homes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/homeassert"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Deps wires the handlers.
type Deps struct {
	Servers *stores.Servers
	Users   *stores.Users
	// Signer signs assertions; nil ⇒ the cloud has no assertion key and
	// answers 503 (linked sign-in unavailable).
	Signer *homeassert.Signer
	// RelayHost is the relay's public apex: a home's relay address is
	// https://<slug>.<RelayHost>.
	RelayHost string
	// WebURL is the cloud web app's origin, where a person confirms a link
	// (its /link-home page).
	WebURL string
	// Direct lists a home's HTTPS direct name once its outside check passed (story 50.15);
	// nil: direct access off.
	Direct *stores.Direct
}

// MountUser registers the routes behind the user auth group.
func MountUser(r chi.Router, d *Deps) {
	r.Get("/v1/homes", d.List)
	r.Post("/v1/homes/{id}/assertion", d.Assertion)
}

// MountPublic registers the public key route (no auth).
func MountPublic(r chi.Router, d *Deps) {
	r.Get("/v1/homes/assertion-key", d.Key)
	r.Get("/v1/homes/{id}/link", d.Link)
}

var (
	serverIDShape = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)
	nonceShape    = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
)

// linkReturn is the home page a confirmed link goes back to: an http(s) address with
// a host and no credentials in it. (The assertion it carries is bound to the nonce of
// the home user who started, so it links nothing for anyone else.)
func linkReturn(raw string) (string, bool) {
	if raw == "" || len(raw) > 2048 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	u.Fragment = ""
	return u.String(), true
}

// Link starts linking from a home (its Account page): the home made a nonce for its
// signed-in user (POST /api/me/cloud-link/start) and sends the browser here with it.
// This answers a redirect to the cloud web's /link-home page, which signs the person
// in to the cloud, asks them to confirm, then asks for the link assertion and hands it
// back to the home. The redirect goes to this cloud's own web app only, so it is never
// an open redirect; nothing is signed here.
func (d *Deps) Link(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	q := r.URL.Query()
	nonce := q.Get("nonce")
	ret, ok := linkReturn(q.Get("return"))
	switch {
	case !serverIDShape.MatchString(id):
		writeErr(w, http.StatusBadRequest, "bad_server", "not a server id")
		return
	case !nonceShape.MatchString(nonce):
		writeErr(w, http.StatusBadRequest, "bad_nonce", "not a link nonce")
		return
	case !ok:
		writeErr(w, http.StatusBadRequest, "bad_return", "the return address must be an http(s) address")
		return
	case d.WebURL == "":
		writeErr(w, http.StatusServiceUnavailable, "link_unavailable", "this cloud has no web app to confirm a link")
		return
	}
	target := strings.TrimRight(d.WebURL, "/") + "/link-home?" +
		url.Values{"server": {id}, "nonce": {nonce}, "return": {ret}}.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, target, http.StatusFound)
}

type homeView struct {
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	// RelayURL is the home's address through the relay; DirectURL its
	// direct address when it reported one (story 25.10: try it first).
	RelayURL  string `json:"relay_url"`
	DirectURL string `json:"direct_url,omitempty"`
	// Connections is every way to reach the home, in the order apps try them (story 50.13):
	// local, local_ip (50.15 §3), direct, relay hint, relay. Apps save it and use it while the cloud is down. Until
	// 50.15 and 50.16 it holds the direct address and the relay; a home whose plan has no relay
	// lists no relay (50.19). DirectURL and RelayURL stay for apps from before it.
	Connections []connection `json:"connections"`
	Online      bool         `json:"online"`
	// Linked: the home linked this account to one of its users (sign-in
	// works). Owned: this account holds the home's cloud registration,
	// which by itself is no account inside the home.
	Linked bool `json:"linked"`
	Owned  bool `json:"owned"`
}

// connection is one way to reach a home: kind is local, local_ip, direct, relay_hint or relay.
// A local_ip entry is a LAN address by its IP (story 50.15 §3: a router blocking DNS rebinding
// refuses the LAN name); TLSName is the name its certificate is checked against there, one of
// the home's own names, so the app still proves it reached this home.
type connection struct {
	URL     string `json:"url"`
	Kind    string `json:"kind"`
	TLSName string `json:"tls_name,omitempty"`
}

// localConnections are a home's LAN entries (story 50.15 §3): each address's name under the
// home's certificate first, then each raw address pinned to that name. Apps from before
// local_ip leave it out (a kind they don't know).
func localConnections(h stores.LANHome) []connection {
	names := make([]connection, 0, len(h.LAN))
	raw := make([]connection, 0, len(h.LAN))
	for _, a := range h.LAN {
		name := direct.Name(a.Addr(), h.HomeID, h.Domain)
		port := strconv.Itoa(int(a.Port()))
		names = append(names, connection{URL: "https://" + net.JoinHostPort(name, port), Kind: "local"})
		raw = append(raw, connection{URL: "https://" + net.JoinHostPort(a.Addr().String(), port), Kind: "local_ip", TLSName: name})
	}
	return append(names, raw...)
}

// connectionsFor is a home's connections in the order apps try them (story 50.13 §2). secure is
// its HTTPS direct name (story 50.15), listed first when its outside check passed; the plain
// HTTP direct address stays for older apps.
func connectionsFor(directURL, relayURL string, relay bool, secure ...string) []connection {
	out := []connection{}
	for _, u := range secure {
		if u != "" {
			out = append(out, connection{URL: u, Kind: "direct"})
		}
	}
	if directURL != "" {
		out = append(out, connection{URL: directURL, Kind: "direct"})
	}
	if relay && relayURL != "" {
		out = append(out, connection{URL: relayURL, Kind: "relay"})
	}
	return out
}

// Connection is one way to reach a home, as apps (and a TV given a cloud code, story 58.03)
// receive it.
type Connection = connection

// Reach is how apps reach one home (story 50.13): its relay and direct addresses and its
// connections in the order apps try them.
type Reach struct {
	RelayURL    string
	DirectURL   string
	Connections []Connection
}

// ReachFor is each home's Reach, by server id. A failed lookup of the direct entries leaves them
// out (the relay and the plain direct address still go).
func (d *Deps) ReachFor(ctx context.Context, hs []stores.Server) map[string]Reach {
	// story 50.15: the homes whose direct name passed its outside check
	reach := map[string]stores.Reachable{}
	lan := map[string]stores.LANHome{}
	if d.Direct != nil {
		ids := make([]string, 0, len(hs))
		for _, h := range hs {
			ids = append(ids, h.ID)
		}
		if found, err := d.Direct.ReachableFor(ctx, ids); err == nil {
			reach = found
		}
		// and their LAN entries (50.15 §3)
		if found, err := d.Direct.LANFor(ctx, ids); err == nil {
			lan = found
		}
	}
	out := make(map[string]Reach, len(hs))
	for _, h := range hs {
		var v Reach
		if d.RelayHost != "" {
			v.RelayURL = "https://" + h.Slug + "." + d.RelayHost
		}
		if h.DirectIP.Valid && h.DirectPort.Valid {
			host := h.DirectIP.String
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			v.DirectURL = "http://" + host + ":" + strconv.FormatInt(h.DirectPort.Int64, 10)
		}
		secure := ""
		if rc, ok := reach[h.ID]; ok {
			secure = "https://" + net.JoinHostPort(direct.Name(rc.IP, rc.HomeID, rc.Domain), strconv.Itoa(rc.Port))
		}
		v.Connections = connectionsFor(v.DirectURL, v.RelayURL, billing.RelayIncluded(h.Plan), secure)
		if l, ok := lan[h.ID]; ok {
			v.Connections = append(localConnections(l), v.Connections...)
		}
		out[h.ID] = v
	}
	return out
}

// List answers the homes this cloud account reaches.
func (d *Deps) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	hs, err := d.Servers.HomesFor(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", "")
		return
	}
	servers := make([]stores.Server, 0, len(hs))
	for _, h := range hs {
		servers = append(servers, h.Server)
	}
	reach := d.ReachFor(r.Context(), servers)
	out := make([]homeView, 0, len(hs))
	for _, h := range hs {
		rc := reach[h.ID]
		out = append(out, homeView{ServerID: h.ID, Name: h.Name, Slug: h.Slug, Online: h.Online, Linked: h.Linked, Owned: h.Owned,
			RelayURL: rc.RelayURL, DirectURL: rc.DirectURL, Connections: rc.Connections})
	}
	writeJSON(w, http.StatusOK, map[string]any{"homes": out})
}

type assertionReq struct {
	// Purpose is "signin" (default) or "link".
	Purpose string `json:"purpose"`
	// Nonce is the home's link nonce (purpose link).
	Nonce string `json:"nonce"`
}

type assertionResp struct {
	Assertion string    `json:"assertion"`
	ExpiresAt time.Time `json:"expires_at"`
	ServerID  string    `json:"server_id"`
	RelayURL  string    `json:"relay_url,omitempty"`
}

// Assertion signs a one-use assertion for the caller and one home. A
// sign-in assertion is only for a home that linked this account; a link
// assertion is for any home (the home binds it to the home user who
// started the link with its nonce, and refuses it otherwise).
func (d *Deps) Assertion(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	if d.Signer == nil {
		writeErr(w, http.StatusServiceUnavailable, "assertions_unavailable", "this cloud has no assertion key")
		return
	}
	var req assertionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid_body", "")
		return
	}
	if req.Purpose == "" {
		req.Purpose = homeassert.PurposeSignIn
	}
	if req.Purpose != homeassert.PurposeSignIn && req.Purpose != homeassert.PurposeLink {
		writeErr(w, http.StatusUnprocessableEntity, "bad_purpose", "purpose is signin or link")
		return
	}
	if req.Purpose == homeassert.PurposeLink && (len(req.Nonce) < 16 || len(req.Nonce) > 128) {
		writeErr(w, http.StatusUnprocessableEntity, "bad_nonce", "a link needs the home's nonce")
		return
	}
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil || u.Status != stores.StatusActive {
		writeErr(w, http.StatusForbidden, "account_inactive", "")
		return
	}
	sv, err := d.Servers.BySlugOrID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	label := ""
	if req.Purpose == homeassert.PurposeSignIn {
		linked, err := d.Servers.IsLinked(r.Context(), sv.ID, uid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "lookup_failed", "")
			return
		}
		if !linked {
			// the same answer as an unknown home: nothing about a home the
			// account is not linked to
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
	} else {
		label = u.Email
	}
	tok, p, err := d.Signer.Issue(req.Purpose, uid, sv.ID, req.Nonce, label)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "sign_failed", "")
		return
	}
	resp := assertionResp{Assertion: tok, ExpiresAt: time.Unix(p.Exp, 0).UTC(), ServerID: sv.ID}
	if d.RelayHost != "" {
		resp.RelayURL = "https://" + sv.Slug + "." + d.RelayHost
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

// Key publishes the assertion public key. A home pins the key it got at
// claim; one claimed before assertions existed fetches it here once.
func (d *Deps) Key(w http.ResponseWriter, _ *http.Request) {
	if d.Signer == nil {
		writeErr(w, http.StatusServiceUnavailable, "assertions_unavailable", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"alg": "Ed25519", "public_key": d.Signer.PublicKeyB64()})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
