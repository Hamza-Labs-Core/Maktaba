package cluster

import (
	"context"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

// Advertiser announces the home on the LAN (design §2). It carries no
// library or user name: only the instance name, its short id, the API
// version, the schema revision and whether joins are open.
type Advertiser struct {
	Publisher discovery.Publisher
	Port      int
	Version   string
	SchemaRev int
}

// Service builds the announcement for id.
func (a *Advertiser) Service(id Identity) discovery.Service {
	join := "0"
	if id.AcceptJoins {
		join = "1"
	}
	return discovery.Service{
		Instance: "Maktaba (" + id.Name + ")",
		Domain:   "local.",
		Port:     a.Port,
		TXT: map[string]string{
			"protocol":   "1",
			"role":       "home",
			"id":         id.Short(),
			"version":    a.Version,
			"schema_rev": strconv.Itoa(a.SchemaRev),
			"join":       join,
		},
	}
}

// Announce (re-)publishes the home. A nil Advertiser is a no-op.
func (a *Advertiser) Announce(ctx context.Context, id Identity) error {
	if a == nil || a.Publisher == nil {
		return nil
	}
	return a.Publisher.Publish(ctx, a.Service(id))
}
