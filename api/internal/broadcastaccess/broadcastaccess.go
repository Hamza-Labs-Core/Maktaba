// Package broadcastaccess is the home's side of who may watch its broadcast channels (Epic 50
// story 50.26): each channel's access (invite or open), the home's viewer limits, its invite
// links and codes and the devices on each invite. The cloud keeps all of it and signs the
// invites; the home passes its people's calls through its relay agent, which adds the server's
// secret (cloud/internal/cloudlink.BroadcastAccessHandler).
//
// The rule is the home's own: only an owner or an admin may read or change any of it
// (authmw.RequireAdmin, enforced here, not in the page). A cloud sign-in with the play scope is
// never an admin (principal.ApplyCloudScope), so it is refused too. The cloud then holds the
// home to its own channels: an invite grants only the home's channels it names.
package broadcastaccess

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Prefix is where the routes live on the home's API.
const Prefix = "/api/broadcast/access"

// MaxBody is the largest request passed on (an invite or a setting).
const MaxBody = 64 << 10

var idRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Handler passes the calls to the relay agent.
type Handler struct {
	// AgentAddr is the relay agent's loopback admin address; "" reads
	// MAKTABA_CLOUDLINK_ADMIN, and none means the home is not linked to the cloud.
	AgentAddr string
	Client    *http.Client
}

func (h *Handler) agentAddr() string {
	if h.AgentAddr != "" {
		return h.AgentAddr
	}
	return os.Getenv("MAKTABA_CLOUDLINK_ADMIN")
}

// Mount registers the routes, each owner or admin only:
//
//	GET    /api/broadcast/access                              the channels' access, limits, verification
//	PUT    /api/broadcast/access/channels/{id}                a channel's access and viewer cap
//	PUT    /api/broadcast/access/limits                       devices per network, daily minutes
//	GET    /api/broadcast/access/invites                      the invites and their uses
//	POST   /api/broadcast/access/invites                      a new invite link and code
//	DELETE /api/broadcast/access/invites/{id}                 withdraw an invite
//	GET    /api/broadcast/access/invites/{id}/devices         the devices on an invite
//	DELETE /api/broadcast/access/invites/{id}/devices/{pass}  remove one device
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(authmw.RequireAdmin)
		r.Get(Prefix, h.pass("access"))
		r.Put(Prefix+"/channels/{id}", h.pass("channels/{id}/access"))
		r.Put(Prefix+"/limits", h.pass("viewer-limits"))
		r.Get(Prefix+"/invites", h.pass("invites"))
		r.Post(Prefix+"/invites", h.pass("invites"))
		r.Delete(Prefix+"/invites/{id}", h.pass("invites/{id}"))
		r.Get(Prefix+"/invites/{id}/devices", h.pass("invites/{id}/devices"))
		r.Delete(Prefix+"/invites/{id}/devices/{pass}", h.pass("invites/{id}/devices/{pass}"))
	})
}

var errUnlinked = errors.New("the home is not linked to Maktaba Cloud")

// pass sends the call to the agent's route (with {id} and {pass} filled in) and the answer back.
func (h *Handler) pass(route string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub := route
		for _, k := range []string{"id", "pass"} {
			if v := chi.URLParam(r, k); v != "" {
				if !idRE.MatchString(v) {
					httperror.Write(w, r, httperror.BadRequest("bad "+k))
					return
				}
				sub = strings.Replace(sub, "{"+k+"}", url.PathEscape(v), 1)
			}
		}
		var body io.Reader
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			body = http.MaxBytesReader(w, r.Body, MaxBody)
		}
		status, ctype, out, err := h.agent(r.Context(), r.Method, sub, body)
		if errors.Is(err, errUnlinked) {
			httperror.Write(w, r, (&httperror.Error{Type: "about:blank", Title: "Link the home to Maktaba Cloud first",
				Status: http.StatusConflict}).With("code", "cloud_not_linked"))
			return
		}
		if err != nil {
			httperror.Write(w, r, (&httperror.Error{Type: "about:blank", Title: "Maktaba Cloud is out of reach",
				Status: http.StatusBadGateway}).With("code", "cloud_unreachable"))
			return
		}
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write(out)
	}
}

func (h *Handler) agent(ctx context.Context, method, sub string, body io.Reader) (int, string, []byte, error) {
	addr := h.agentAddr()
	if addr == "" {
		return 0, "", nil, errUnlinked
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+"/admin/cloud-link/broadcast/"+sub, body)
	if err != nil {
		return 0, "", nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cl := h.Client
	if cl == nil {
		cl = &http.Client{Timeout: 25 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, "", nil, err
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), out, nil
}
