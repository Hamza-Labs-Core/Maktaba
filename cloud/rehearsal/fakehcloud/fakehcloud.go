// Package fakehcloud is a fake of the two Hetzner Cloud API calls the
// failover makes (Epic 50 story 50.06): assign a Floating IP to a server, and
// read which server holds it. "Moving the IP" moves a Docker network alias
// (relay.test) to the assigned container, so whatever dials the name follows
// the IP, as the real Floating IP does. The rehearsal points HCLOUD_ENDPOINT
// at it: no Hetzner host is ever contacted.
//
//	POST /v1/floating_ips/{id}/actions/assign  {"server": <server id>}
//	GET  /v1/floating_ips/{id}
package fakehcloud

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mover moves the IP's network alias to a container (Docker in the
// rehearsal, a recorder in tests).
type Mover interface {
	Move(server int64) error
}

// API is the fake. Servers maps a Hetzner server id to its container.
type API struct {
	Mover Mover
	// Delay is how long an assignment takes before the IP answers on its new
	// holder (default 5 s; a scenario uses 60 s, the worst reported).
	Delay time.Duration
	// Token, when set, must be the bearer of every request.
	Token string

	mu      sync.Mutex
	holder  map[int64]int64 // floating ip -> server (0: none)
	actions int64
	moving  sync.WaitGroup
}

// New is the fake with every IP unassigned.
func New(m Mover, delay time.Duration) *API {
	return &API{Mover: m, Delay: delay, holder: map[int64]int64{}}
}

// Holder is the server the IP is assigned to.
func (a *API) Holder(ip int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.holder[ip]
}

// Wait waits for the moves in flight (tests).
func (a *API) Wait() { a.moving.Wait() }

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.Token != "" && r.Header.Get("Authorization") != "Bearer "+a.Token {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "unauthorized", "message": "invalid token"}})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// v1 floating_ips {id} [actions assign]
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "floating_ips" {
		http.NotFound(w, r)
		return
	}
	ip, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case len(parts) == 3 && r.Method == http.MethodGet:
		a.mu.Lock()
		h := a.holder[ip]
		a.mu.Unlock()
		var server any
		if h != 0 {
			server = h
		}
		writeJSON(w, http.StatusOK, map[string]any{"floating_ip": map[string]any{"id": ip, "server": server}})
	case len(parts) == 5 && parts[3] == "actions" && parts[4] == "assign" && r.Method == http.MethodPost:
		var body struct {
			Server int64 `json:"server"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Server == 0 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]string{"code": "invalid_input", "message": "server required"}})
			return
		}
		a.mu.Lock()
		a.actions++
		id := a.actions
		a.mu.Unlock()
		a.moving.Add(1)
		go func() {
			defer a.moving.Done()
			delay := a.Delay
			if delay < 0 {
				delay = 0
			}
			time.Sleep(delay)
			if a.Mover != nil {
				if err := a.Mover.Move(body.Server); err != nil {
					return
				}
			}
			a.mu.Lock()
			a.holder[ip] = body.Server
			a.mu.Unlock()
		}()
		writeJSON(w, http.StatusCreated, map[string]any{"action": map[string]any{
			"id": id, "command": "assign_floating_ip", "status": "running",
			"resources": []map[string]any{{"id": ip, "type": "floating_ip"}, {"id": body.Server, "type": "server"}},
		}})
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
