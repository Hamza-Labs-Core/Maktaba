// Package fakeacmedns is the cloud rehearsal's DNS-01 provider (Epic 50 story
// 50.02, scenario cert-on-standby): an acme-dns API (POST /update, what Caddy's
// dns.providers.acmedns calls) and a DNS server that answers the TXT records it
// was given for _acme-challenge.<zone>, which is where Pebble (Let's Encrypt's
// test CA) looks. Production uses Cloudflare's API instead; nothing here leaves
// the runner.
package fakeacmedns

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// keep is how many TXT values are answered at once: the apex and the wildcard of
// each node are solved together, all under one name.
const keep = 8

// Server holds the challenge values.
type Server struct {
	Zone     string // relay.test: the challenge name is _acme-challenge.relay.test.
	User     string // the X-Api-User every update must carry
	Key      string // its X-Api-Key
	mu       sync.Mutex
	values   []entry
	updates  int
	answered int
}

type entry struct {
	txt string
	at  time.Time
}

// Updates is how many TXT values Caddy set.
func (s *Server) Updates() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updates
}

// ServeHTTP is the acme-dns API: POST /update {"subdomain", "txt"}.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && r.URL.Path == "/update":
		if r.Header.Get("X-Api-User") != s.User || r.Header.Get("X-Api-Key") != s.Key {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var body struct {
			Subdomain string `json:"subdomain"`
			TXT       string `json:"txt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TXT == "" {
			http.Error(w, `{"error":"bad_txt"}`, http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.values = append(s.values, entry{txt: body.TXT, at: time.Now()})
		if len(s.values) > keep {
			s.values = s.values[len(s.values)-keep:]
		}
		s.updates++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"txt": body.TXT})
	default:
		http.NotFound(w, r)
	}
}

// Answer builds the DNS reply to a query message.
func (s *Server) Answer(query []byte) ([]byte, error) {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil, err
	}
	q, err := p.Question()
	if err != nil {
		return nil, err
	}
	zone := strings.TrimSuffix(s.Zone, ".") + "."
	want := "_acme-challenge." + zone
	resp := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionDesired: h.RecursionDesired}
	b := dnsmessage.NewBuilder(nil, resp)
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(q); err != nil {
		return nil, err
	}
	if err := b.StartAnswers(); err != nil {
		return nil, err
	}
	name := strings.ToLower(q.Name.String())
	// the zone's SOA, for any name in it: how Caddy finds where to put the record
	if q.Type == dnsmessage.TypeSOA && (name == zone || strings.HasSuffix(name, "."+zone)) {
		zn, err := dnsmessage.NewName(zone)
		if err != nil {
			return nil, err
		}
		ns, _ := dnsmessage.NewName("ns." + zone)
		mbox, _ := dnsmessage.NewName("admin." + zone)
		if err := b.SOAResource(dnsmessage.ResourceHeader{Name: zn, Class: dnsmessage.ClassINET, TTL: 60},
			dnsmessage.SOAResource{NS: ns, MBox: mbox, Serial: 1, Refresh: 60, Retry: 60, Expire: 600, MinTTL: 1}); err != nil {
			return nil, err
		}
	}
	if q.Type == dnsmessage.TypeTXT && name == want {
		s.mu.Lock()
		vals := append([]entry(nil), s.values...)
		s.answered++
		s.mu.Unlock()
		for _, v := range vals {
			if err := b.TXTResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 1},
				dnsmessage.TXTResource{TXT: []string{v.txt}}); err != nil {
				return nil, err
			}
		}
	}
	return b.Finish()
}

// ServeDNS answers queries on a UDP socket until it is closed.
func (s *Server) ServeDNS(pc net.PacketConn) error {
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		out, err := s.Answer(buf[:n])
		if err != nil {
			continue
		}
		_, _ = pc.WriteTo(out, addr)
	}
}
