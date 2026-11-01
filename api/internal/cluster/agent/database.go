package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// PushDatabase hands every connected machine this home's database CA over the machine
// channel (or the home's word that its database has no TLS: caFile ""), which the
// cluster key authenticates (specs/epics/37-node-replica): a machine verifies its
// home's database with it, never with what the database connection itself would say.
// Each machine's agent session is told once, as soon as it connects, and again when the
// CA changes (looked at every interval); it runs until ctx ends. A machine that waited
// for the next interval instead would run that long unable to verify its home's database
// (its copy idle), then start again in the middle of its work.
func (h *Hub) PushDatabase(ctx context.Context, caFile string, every time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	told := map[string][32]byte{} // agent session -> what it was told
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		joined := h.agentJoinedCh()
		if args, err := databaseArgs(caFile); err != nil {
			logger.Warn("cluster: this home's database CA cannot be read for the machines", "err", err,
				"event", "cluster_database_ca_unreadable")
		} else {
			sum := sha256.Sum256(args)
			live := map[string]bool{}
			for node, sid := range h.MachineAgents() {
				live[sid] = true
				if told[sid] == sum {
					continue
				}
				res, err := h.CommandWithin(ctx, sid, "home.database", args, "system", 30*time.Second)
				if err == nil && res.OK {
					told[sid] = sum
				} else if ctx.Err() == nil {
					logger.Debug("cluster: a machine did not take this home's database CA", "node_id", node,
						"err", err, "event", "cluster_database_ca_not_taken")
				}
			}
			for sid := range told {
				if !live[sid] {
					delete(told, sid)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-joined:
		}
	}
}

func databaseArgs(caFile string) (json.RawMessage, error) {
	if caFile == "" {
		return json.RawMessage(`{"tls_off":true}`), nil
	}
	b, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		// one being written: a machine told an empty CA could verify nothing
		return nil, fmt.Errorf("%s is empty", caFile)
	}
	return json.Marshal(map[string]string{"ca_pem": string(b)})
}
