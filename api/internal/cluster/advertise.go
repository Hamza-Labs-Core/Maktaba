package cluster

import (
	"context"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

// Advertiser announces the home on the LAN (design §2). It carries no
// library or user name: only the instance name, its id, the API
// version, the schema revision and whether joins are open.
//
// The apps' home picker (Epic 42 story 42.2) reads `home` (the home's instance id, the
// same as GET /api/discovery/home's `id`; `id` is the announcing machine's own, here the
// same), `name` (its display name), `path` (the web base path, only when it is not
// "/") and `tls` (1 or 0); they dedupe by `home`, then `id`, then host:port.
type Advertiser struct {
	Publisher discovery.Publisher
	Port      int
	Version   string
	SchemaRev int
	// Path is the web base path ("" or "/": none is announced).
	Path string
	// TLS: the home's web address is https (TXT tls=1, else tls=0).
	TLS bool
}

// Service builds the announcement for id.
func (a *Advertiser) Service(id Identity) discovery.Service {
	join := "0"
	if id.AcceptJoins {
		join = "1"
	}
	tls := "0"
	if a.TLS {
		tls = "1"
	}
	txt := map[string]string{
		"protocol":   "1",
		"role":       "home",
		"id":         strings.ToLower(id.InstanceID),
		"home":       strings.ToLower(id.InstanceID),
		"tls":        tls,
		"version":    a.Version,
		"schema_rev": strconv.Itoa(a.SchemaRev),
		"join":       join,
	}
	if id.Name != "" {
		txt["name"] = id.Name
	}
	if p := strings.TrimRight(strings.TrimSpace(a.Path), "/"); p != "" {
		txt["path"] = p
	}
	return discovery.Service{
		Instance: "Maktaba (" + id.Name + ")",
		Domain:   "local.",
		Port:     a.Port,
		TXT:      txt,
	}
}

// Announce (re-)publishes the home. A nil Advertiser is a no-op.
func (a *Advertiser) Announce(ctx context.Context, id Identity) error {
	if a == nil || a.Publisher == nil {
		return nil
	}
	return a.Publisher.Publish(ctx, a.Service(id))
}
