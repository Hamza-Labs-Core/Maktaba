package mdns

// Every machine of a cluster advertises itself (Epic 35 story 35.11, design §12): the
// supervisor is the one advertiser of a machine (the API's own advertiser runs only
// when the API runs without a supervisor). TXT keys, never a library or user name:
//
//	protocol=1  role=home|node  id=<this machine, 8 hex>  home=<the home's instance, 8 hex>
//	web=<web port>  version=<release>  name=<machine name>
//	home only:  schema_rev=<n>  join=0|1
//
// plus the older api=/api and path=/ the apps already read.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"
)

// Machine is what a machine announces.
type Machine struct {
	Role      string // home | node
	ID        string // this machine's id (instance id or node id); 8 hex are sent
	HomeID    string // the home's instance id (equal to ID on the home)
	Name      string
	WebPort   int
	Version   string
	SchemaRev int  // home only
	Join      bool // home only
}

// Short is the first 8 hex digits of an id (dashes dropped, lower case).
func Short(id string) string {
	h := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(id), "-", ""))
	if len(h) > 8 {
		h = h[:8]
	}
	return h
}

// TXT is the machine's TXT record (sorted key=value).
func (m Machine) TXT() []string {
	kv := map[string]string{
		"protocol": "1",
		"role":     m.Role,
		"version":  m.Version,
		"api":      "/api",
		"path":     "/",
	}
	if m.ID != "" {
		kv["id"] = Short(m.ID)
	}
	if m.HomeID != "" {
		kv["home"] = Short(m.HomeID)
	}
	if m.WebPort > 0 {
		kv["web"] = strconv.Itoa(m.WebPort)
	}
	if m.Name != "" {
		kv["name"] = m.Name
	}
	if m.Role == "home" {
		kv["schema_rev"] = strconv.Itoa(m.SchemaRev)
		kv["join"] = "0"
		if m.Join {
			kv["join"] = "1"
		}
	}
	out := make([]string, 0, len(kv))
	for k, v := range kv {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// Instance is the visible service name: "Maktaba (<name>)".
func (m Machine) Instance() string {
	n := m.Name
	if n == "" {
		n = "maktaba"
	}
	return "Maktaba (" + n + ")"
}

// Publisher keeps one publication and re-publishes when the machine changes.
type Publisher struct {
	mu        sync.Mutex
	server    *zeroconf.Server
	published bool
	last      Machine
	port      int
	// register is zeroconf.Register (tests replace it)
	register func(instance, service, domain string, port int, txt []string) (*zeroconf.Server, error)
}

// NewPublisher returns a publisher that has announced nothing yet.
func NewPublisher() *Publisher {
	return &Publisher{register: func(instance, service, domain string, port int, txt []string) (*zeroconf.Server, error) {
		return zeroconf.Register(instance, service, domain, port, txt, nil)
	}}
}

// Publish announces m on port (the API port), replacing the TXT when only it changed.
// It reports whether anything changed.
func (p *Publisher) Publish(m Machine, port int) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.published && p.port == port && p.last.Instance() == m.Instance() {
		if strings.Join(p.last.TXT(), ",") == strings.Join(m.TXT(), ",") {
			return false, nil
		}
		if p.server != nil {
			p.server.SetText(m.TXT())
		}
		p.last = m
		return true, nil
	}
	if p.server != nil {
		p.server.Shutdown()
		p.server = nil
	}
	p.published = false
	srv, err := p.register(m.Instance(), "_maktaba._tcp", "local.", port, m.TXT())
	if err != nil {
		return false, fmt.Errorf("register mDNS service: %w", err)
	}
	p.server, p.last, p.port, p.published = srv, m, port, true
	return true, nil
}

// Close withdraws the publication.
func (p *Publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.server != nil {
		p.server.Shutdown()
		p.server = nil
	}
	p.published = false
}
