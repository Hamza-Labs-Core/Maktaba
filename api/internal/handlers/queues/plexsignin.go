package queues

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// "Sign in with Plex" (the plex.tv PIN flow, as dizqueTV does it):
//
//	POST /api/extensions/plex/signin            -> {id, code, auth_url}
//	GET  /api/extensions/plex/signin/{id}       -> {state:"waiting"} | {state:"done", servers:[{name, uri}]}
//	POST /api/extensions/plex/signin/{id}/use   {name} -> the plex Extension (secrets masked)
//
// The account token and the per-server access tokens never leave the
// server: pending sign-ins are held in memory (plexSigninTTL) and only
// the chosen server's token is written to the plex extension settings.

const (
	defaultPlexTVURL = "https://plex.tv"
	plexAuthAppURL   = "https://app.plex.tv/auth"
	plexProduct      = "Maktaba"
	plexSigninTTL    = 15 * time.Minute
	// plexClientIDKey is stored in the plex extension row next to the
	// schema keys; buildExtension only echoes schema keys, so it is
	// never returned (nor is it secret).
	plexClientIDKey = "client_id"
)

type plexServer struct {
	Name  string `json:"name"`
	URI   string `json:"uri"`
	token string
}

type plexSignin struct {
	clientID string
	created  time.Time
	done     bool
	servers  []plexServer
}

type plexSignins struct {
	mu      sync.Mutex
	pending map[string]*plexSignin
}

func (h *Handler) plexTV() string {
	if h.PlexTVURL != "" {
		return strings.TrimRight(h.PlexTVURL, "/")
	}
	return defaultPlexTVURL
}

func (h *Handler) plexClient() *http.Client {
	if h.PlexHTTP != nil {
		return h.PlexHTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// get returns a live pending sign-in and drops expired ones.
func (s *plexSignins) get(id string, now time.Time) *plexSignin {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, p := range s.pending {
		if now.Sub(p.created) > plexSigninTTL {
			delete(s.pending, k)
		}
	}
	return s.pending[id]
}

func (s *plexSignins) put(id string, p *plexSignin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[string]*plexSignin{}
	}
	s.pending[id] = p
}

// isDone reads p.done under the lock the status poll writes it with.
func (s *plexSignins) isDone(p *plexSignin) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return p.done
}

func (s *plexSignins) drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}

// plexRequest calls plex.tv with the standard client headers. token is
// optional (X-Plex-Token).
func (h *Handler) plexRequest(ctx context.Context, method, path, clientID, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, h.plexTV()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", plexProduct)
	req.Header.Set("X-Plex-Client-Identifier", clientID)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	resp, err := h.plexClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return &plexStatusError{status: resp.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

type plexStatusError struct{ status int }

func (e *plexStatusError) Error() string { return "plex.tv answered " + strconv.Itoa(e.status) }

func plexUpstream(w http.ResponseWriter, r *http.Request, err error) {
	httperror.Write(w, r, httperror.BadGateway("plex.tv: "+err.Error()))
}

// plexClientID returns the stable per-install client identifier, creating
// and storing it in the plex extension row on first use. The read-modify-
// write runs under extMu so two first sign-ins agree on one id and a
// concurrent settings PUT is not overwritten.
func (h *Handler) plexClientID(ctx context.Context) (string, error) {
	h.extMu.Lock()
	defer h.extMu.Unlock()
	if h.plexID != "" {
		return h.plexID, nil
	}
	rows, err := h.loadExtensionRows(ctx)
	if err != nil {
		return "", err
	}
	cur := rows["plex"]
	if id, ok := cur.settings[plexClientIDKey].(string); ok && id != "" {
		h.plexID = id
		return id, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := "maktaba-" + hex.EncodeToString(b)
	if cur.settings == nil {
		cur.settings = map[string]any{}
	}
	cur.settings[plexClientIDKey] = id
	blob, _ := json.Marshal(cur.settings)
	if _, err := h.DB.ExecContext(ctx, upsertExtensionSQL, "plex", cur.enabled, string(blob), h.now()); err != nil {
		return "", err
	}
	h.plexID = id
	return id, nil
}

// PlexSignin implements POST /api/extensions/plex/signin.
func (h *Handler) PlexSignin(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	clientID, err := h.plexClientID(r.Context())
	if err != nil {
		writeErr(w, r, "plex client id", err)
		return
	}
	var pin struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	if err := h.plexRequest(r.Context(), http.MethodPost, "/api/v2/pins?strong=true", clientID, "", &pin); err != nil {
		plexUpstream(w, r, err)
		return
	}
	if pin.ID == 0 || pin.Code == "" {
		plexUpstream(w, r, errors.New("no PIN in the answer"))
		return
	}
	id := strconv.FormatInt(pin.ID, 10)
	h.plexPins.put(id, &plexSignin{clientID: clientID, created: h.now()})
	authURL := fmt.Sprintf("%s#?clientID=%s&code=%s&context%%5Bdevice%%5D%%5Bproduct%%5D=%s",
		plexAuthAppURL, url.QueryEscape(clientID), url.QueryEscape(pin.Code), url.QueryEscape(plexProduct))
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"id": id, "code": pin.Code, "auth_url": authURL})
}

type plexConnection struct {
	Protocol string `json:"protocol"`
	URI      string `json:"uri"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay"`
}

type plexResource struct {
	Name        string           `json:"name"`
	Provides    string           `json:"provides"`
	AccessToken string           `json:"accessToken"`
	Connections []plexConnection `json:"connections"`
}

// pickConnection prefers a local https connection, then any local, then
// any https, then anything else; relays are never chosen.
func pickConnection(cs []plexConnection) string {
	best, bestScore := "", -1
	for _, c := range cs {
		if c.Relay || c.URI == "" {
			continue
		}
		score := 0
		if c.Local {
			score += 2
		}
		if c.Protocol == "https" || strings.HasPrefix(c.URI, "https://") {
			score++
		}
		if score > bestScore {
			best, bestScore = c.URI, score
		}
	}
	return best
}

func hasServer(provides string) bool {
	for _, p := range strings.Split(provides, ",") {
		if strings.TrimSpace(p) == "server" {
			return true
		}
	}
	return false
}

// PlexSigninStatus implements GET /api/extensions/plex/signin/{id}.
func (h *Handler) PlexSigninStatus(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	p := h.plexPins.get(id, h.now())
	if p == nil {
		httperror.Write(w, r, httperror.NotFound("plex sign-in "+id))
		return
	}
	if !h.plexPins.isDone(p) {
		var pin struct {
			AuthToken *string `json:"authToken"`
		}
		if err := h.plexRequest(r.Context(), http.MethodGet, "/api/v2/pins/"+url.PathEscape(id), p.clientID, "", &pin); err != nil {
			plexUpstream(w, r, err)
			return
		}
		if pin.AuthToken == nil || *pin.AuthToken == "" {
			common.WriteJSON(w, r, http.StatusOK, map[string]any{"state": "waiting"})
			return
		}
		var res []plexResource
		if err := h.plexRequest(r.Context(), http.MethodGet, "/api/v2/resources?includeHttps=1&includeRelay=0",
			p.clientID, *pin.AuthToken, &res); err != nil {
			plexUpstream(w, r, err)
			return
		}
		servers := []plexServer{}
		for _, rs := range res {
			if !hasServer(rs.Provides) {
				continue
			}
			uri := pickConnection(rs.Connections)
			if uri == "" {
				continue
			}
			servers = append(servers, plexServer{Name: rs.Name, URI: uri, token: rs.AccessToken})
		}
		h.plexPins.mu.Lock()
		p.servers, p.done = servers, true
		h.plexPins.mu.Unlock()
	}
	h.plexPins.mu.Lock()
	servers := append([]plexServer(nil), p.servers...)
	h.plexPins.mu.Unlock()
	if servers == nil {
		servers = []plexServer{}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"state": "done", "servers": servers})
}

// PlexSigninUse implements POST /api/extensions/plex/signin/{id}/use.
func (h *Handler) PlexSigninUse(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Name string `json:"name"`
	}
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	p := h.plexPins.get(id, h.now())
	if p == nil {
		httperror.Write(w, r, httperror.NotFound("plex sign-in "+id))
		return
	}
	h.plexPins.mu.Lock()
	done := p.done
	var chosen *plexServer
	for i := range p.servers {
		if p.servers[i].Name == body.Name {
			s := p.servers[i]
			chosen = &s
			break
		}
	}
	h.plexPins.mu.Unlock()
	if !done {
		httperror.Write(w, r, httperror.Conflict("", "plex sign-in "+id+" is not finished"))
		return
	}
	if chosen == nil {
		httperror.Write(w, r, httperror.BadRequest("unknown server "+body.Name))
		return
	}
	h.extMu.Lock()
	defer h.extMu.Unlock()
	rows, err := h.loadExtensionRows(r.Context())
	if err != nil {
		writeErr(w, r, "extensions", err)
		return
	}
	cur := rows["plex"]
	if cur.settings == nil {
		cur.settings = map[string]any{}
	}
	cur.settings["url"] = chosen.URI
	if chosen.token != "" {
		cur.settings["token"] = chosen.token
	}
	blob, _ := json.Marshal(cur.settings)
	now := h.now()
	if _, err := h.DB.ExecContext(r.Context(), upsertExtensionSQL, "plex", cur.enabled, string(blob), now); err != nil {
		writeErr(w, r, "save extension", err)
		return
	}
	h.plexPins.drop(id)
	def, _ := findExtension("plex")
	ts := now.Format(tsLayout)
	common.WriteJSON(w, r, http.StatusOK,
		buildExtension(def, extRow{enabled: cur.enabled, settings: cur.settings, updated: &ts}))
}
