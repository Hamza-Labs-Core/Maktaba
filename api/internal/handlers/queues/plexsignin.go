package queues

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
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
	httperror.Write(w, r, httperror.BadGateway("plex.tv").Wrap(err))
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

// plexServers lists the Plex Media Servers an account token reaches
// (plex.tv's resources), each with the connection pickConnection prefers
// and that server's own access token.
func (h *Handler) plexServers(ctx context.Context, clientID, accountToken string) ([]plexServer, error) {
	var res []plexResource
	if err := h.plexRequest(ctx, http.MethodGet, "/api/v2/resources?includeHttps=1&includeRelay=0",
		clientID, accountToken, &res); err != nil {
		return nil, err
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
	return servers, nil
}

// usePlexServer makes chosen the plex extension's server: its address in the
// extension row (with clientID, when given, as the install's plex.tv client
// id), its token in home_secrets. It returns the saved row.
func (h *Handler) usePlexServer(ctx context.Context, chosen plexServer, clientID, by string) (extRow, error) {
	h.extMu.Lock()
	defer h.extMu.Unlock()
	rows, err := h.loadExtensionRows(ctx)
	if err != nil {
		return extRow{}, fmt.Errorf("extensions: %w", err)
	}
	cur := rows["plex"]
	if cur.settings == nil {
		cur.settings = map[string]any{}
	}
	cur.settings["url"] = chosen.URI
	if clientID != "" {
		cur.settings[plexClientIDKey] = clientID
		h.plexID = clientID
	}
	// the server's token goes to home_secrets (slot 0120), never the row
	delete(cur.settings, "token")
	if chosen.token != "" {
		if h.Secrets == nil {
			return extRow{}, errNoSecretsStore
		}
		if err := h.Secrets.Put(ctx, secrets.ExtensionName("plex", "token"), []byte(chosen.token), by); err != nil {
			return extRow{}, fmt.Errorf("save secret: %w", err)
		}
	}
	blob, _ := json.Marshal(cur.settings)
	now := h.now()
	if _, err := h.DB.ExecContext(ctx, upsertExtensionSQL, "plex", cur.enabled, string(blob), now); err != nil {
		return extRow{}, fmt.Errorf("save extension: %w", err)
	}
	ts := now.Format(tsLayout)
	cur.updated = &ts
	return cur, nil
}

// errNoSecretsStore: the server runs without its secrets store, so the
// Plex token cannot be kept.
var errNoSecretsStore = errors.New("the secrets store is not available on this server")

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
		servers, err := h.plexServers(r.Context(), p.clientID, *pin.AuthToken)
		if err != nil {
			plexUpstream(w, r, err)
			return
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
	by := ""
	if pr := principal.FromContext(r.Context()); pr != nil {
		by = pr.UserID
	}
	row, err := h.usePlexServer(r.Context(), *chosen, "", by)
	if errors.Is(err, errNoSecretsStore) {
		httperror.Write(w, r, &httperror.Error{Status: http.StatusServiceUnavailable,
			Title: "Service Unavailable", Detail: errNoSecretsStore.Error()})
		return
	}
	if err != nil {
		writeErr(w, r, "plex", err)
		return
	}
	h.plexPins.drop(id)
	def, _ := findExtension("plex")
	common.WriteJSON(w, r, http.StatusOK, buildExtension(def, row, h.secretSet(r.Context(), def)))
}

// PlexLinked is what LinkPlex chose.
type PlexLinked struct {
	Server string `json:"server"`
	URL    string `json:"url"`
	// Servers are every server the account reaches (setup shows them; the
	// owner picks another in Settings › Extensions › Plex).
	Servers []string `json:"servers"`
}

// LinkPlex is "Sign in with Plex" for the desktop app's first-run setup
// (Epic 38 story 38.6, `maktaba-api plex-link`): setup ran the PIN flow
// before this install had a database, with clientID as its plex.tv client id,
// and hands over the account token. The server named prefer is used when the
// account reaches it; else the first with a local connection, else the first.
// The account token itself is never stored, as with the PIN flow above.
func (h *Handler) LinkPlex(ctx context.Context, clientID, accountToken, prefer string) (PlexLinked, error) {
	servers, err := h.plexServers(ctx, clientID, accountToken)
	if err != nil {
		return PlexLinked{}, fmt.Errorf("plex.tv: %w", err)
	}
	if len(servers) == 0 {
		return PlexLinked{}, errors.New("this Plex account reaches no Plex Media Server")
	}
	chosen := servers[0]
	for _, s := range servers {
		if s.Name == prefer {
			chosen = s
			break
		}
	}
	if prefer == "" || chosen.Name != prefer {
		for _, s := range servers {
			if isLocalURI(s.URI) {
				chosen = s
				break
			}
		}
	}
	if _, err := h.usePlexServer(ctx, chosen, clientID, ""); err != nil {
		return PlexLinked{}, err
	}
	names := make([]string, 0, len(servers))
	for _, s := range servers {
		names = append(names, s.Name)
	}
	return PlexLinked{Server: chosen.Name, URL: chosen.URI, Servers: names}, nil
}

// isLocalURI: an address on this network (a private IPv4 or the
// plex.direct name of one), as pickConnection's local connections are.
func isLocalURI(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil {
		return false
	}
	host := u.Hostname()
	// <a-b-c-d>.<hash>.plex.direct carries the address in its first label
	if strings.HasSuffix(host, ".plex.direct") {
		host = strings.ReplaceAll(strings.SplitN(host, ".", 2)[0], "-", ".")
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}
