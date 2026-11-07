package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/url"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/directaccess"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// directService builds direct remote access (Epic 50 story 50.15) on the home that runs the
// relay agent: the supervisor passes the agent's admin address, the listener, the key's
// directory and the web port (MAKTABA_DIRECT_*). nil elsewhere (a machine, a home without the
// cloud link): cloud.direct's loops are then not declared.
func directService(db *sql.DB, agentAddr string, logger *slog.Logger) *directaccess.Service {
	dir, listen := os.Getenv("MAKTABA_DIRECT_DIR"), os.Getenv("MAKTABA_DIRECT_LISTEN")
	up, err := url.Parse(os.Getenv("MAKTABA_DIRECT_UPSTREAM"))
	if agentAddr == "" || dir == "" || listen == "" || err != nil || up.Host == "" {
		return nil
	}
	ids := &cloudlink.IdentitySource{Path: os.Getenv("MAKTABA_CLOUD_IDENTITY")}
	return &directaccess.Service{
		Files:    &directaccess.Files{Dir: dir},
		Agent:    &directaccess.Agent{Addr: agentAddr},
		Listen:   listen,
		Upstream: up,
		ServerID: func() string {
			id, _, _ := ids.Load()
			return id.ServerID
		},
		Settings: func(ctx context.Context) (directaccess.Settings, error) { return directSettings(ctx, db) },
		Log:      logger.With("component", "cloud.direct"),
	}
}

// directSettings reads rules.direct. Offline (#640) direct access stays off: no mapping, no
// listener, nothing to the cloud.
func directSettings(ctx context.Context, db *sql.DB) (directaccess.Settings, error) {
	if ports.Offline() {
		return directaccess.Settings{}, nil
	}
	m, err := rules.Effective(ctx, db, "direct")
	if err != nil {
		return directaccess.Settings{}, err
	}
	var s directaccess.Settings
	s.Enabled, _ = m["enabled"].(bool)
	s.MapPort, _ = m["map_port"].(bool)
	if n, ok := m["manual_port"].(json.Number); ok {
		if v, err := n.Int64(); err == nil && v > 0 && v <= 65535 {
			s.ManualPort = int(v)
		}
	}
	return s, nil
}
