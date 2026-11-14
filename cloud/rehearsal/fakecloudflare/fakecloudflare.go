// Package fakecloudflare is a fake of the Cloudflare DNS record calls relay-dns makes (Epic 50
// story 50.16): list a name's A records, create one, delete one, in one zone. The unit tests and
// the cloud rehearsal point relay-dns at it: no Cloudflare host is ever contacted.
//
//	GET    /zones/{zone}/dns_records?type=A&name=<name>
//	POST   /zones/{zone}/dns_records          {"type":"A","name":…,"content":…,"ttl":…}
//	DELETE /zones/{zone}/dns_records/{id}
//
// Down makes every call answer 503 (Cloudflare's API away).
package fakecloudflare

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// API is the fake zone.
type API struct {
	Zone  string
	Token string // when set, the bearer every call must carry

	mu      sync.Mutex
	records map[string]Record
	next    int
	down    bool
	calls   int
}

// Record is one record of the zone.
type Record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

// New is an empty zone.
func New(zone, token string) *API {
	return &API{Zone: zone, Token: token, records: map[string]Record{}}
}

// Seed adds an A record (the zone as the owner set it up).
func (a *API) Seed(name, ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.add(Record{Type: "A", Name: name, Content: ip, TTL: 60})
}

func (a *API) add(r Record) Record {
	a.next++
	r.ID = fmt.Sprintf("rec%04d", a.next)
	a.records[r.ID] = r
	return r
}

// SetDown makes the API answer 503 (true) or answer again.
func (a *API) SetDown(down bool) {
	a.mu.Lock()
	a.down = down
	a.mu.Unlock()
}

// IPs is name's A record addresses, sorted.
func (a *API) IPs(name string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, r := range a.records {
		if r.Type == "A" && r.Name == name {
			out = append(out, r.Content)
		}
	}
	sort.Strings(out)
	return out
}

// Records is every record of name.
func (a *API) Records(name string) []Record {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Record
	for _, r := range a.records {
		if r.Name == name {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func reply(w http.ResponseWriter, code int, result any, errs ...string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	e := []map[string]any{}
	for _, m := range errs {
		e = append(e, map[string]any{"code": code, "message": m})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": code/100 == 2, "errors": e, "result": result})
}

// ServeHTTP answers the three calls.
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.down {
		reply(w, http.StatusServiceUnavailable, nil, "service unavailable")
		return
	}
	if a.Token != "" && r.Header.Get("Authorization") != "Bearer "+a.Token {
		reply(w, http.StatusForbidden, nil, "authentication error")
		return
	}
	prefix := "/zones/" + a.Zone + "/dns_records"
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == prefix && r.Method == http.MethodGet:
		typ, name := r.URL.Query().Get("type"), r.URL.Query().Get("name")
		out := []Record{}
		for _, rec := range a.records {
			if (typ == "" || rec.Type == typ) && (name == "" || rec.Name == name) {
				out = append(out, rec)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		reply(w, http.StatusOK, out)
	case path == prefix && r.Method == http.MethodPost:
		var rec Record
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil || rec.Type != "A" || rec.Name == "" || rec.Content == "" {
			reply(w, http.StatusBadRequest, nil, "bad record")
			return
		}
		for _, cur := range a.records {
			if cur.Type == rec.Type && cur.Name == rec.Name && cur.Content == rec.Content {
				reply(w, http.StatusBadRequest, nil, "an identical record already exists")
				return
			}
		}
		reply(w, http.StatusOK, a.add(rec))
	case strings.HasPrefix(path, prefix+"/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(path, prefix+"/")
		if _, ok := a.records[id]; !ok {
			reply(w, http.StatusNotFound, nil, "record not found")
			return
		}
		delete(a.records, id)
		reply(w, http.StatusOK, map[string]string{"id": id})
	default:
		reply(w, http.StatusNotFound, nil, "no route")
	}
}
