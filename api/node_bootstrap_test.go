package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Epic 51: a joined machine's API runner follows that machine's desired state (its
// node id), the home's the home's.
func TestTheAPIRunnerFollowsThisMachine(t *testing.T) {
	node := nodemode.Config{Enabled: true, NodeID: "4b1d0c3e-node"}
	if got := components.NewRunnerFor(slog.New(slog.NewTextHandler(io.Discard, nil)), nodeMachine(node)).Machine(); got != node.NodeID {
		t.Fatalf("a node's runner follows %q", got)
	}
	if got := components.NewRunnerFor(slog.New(slog.NewTextHandler(io.Discard, nil)), nodeMachine(nodemode.Config{})).Machine(); got != store.Home {
		t.Fatalf("the home's runner follows %q", got)
	}
}

// A full node's pools on the home's database each have a cap: a pool without one kept every
// connection it ever opened, against the home's connection slots.
func TestOpenNodeDBsCapsThePools(t *testing.T) {
	const dsn = "postgres://maktaba_node_x@127.0.0.1:1/maktaba?sslmode=disable"
	d, err := openNodeDBs(dsn, dsn, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	if got := d.home.Stats().MaxOpenConnections; got != nodeHomeMaxOpen {
		t.Fatalf("home pool: MaxOpenConnections = %d, want %d", got, nodeHomeMaxOpen)
	}
	if got := d.copyDB.Stats().MaxOpenConnections; got != nodeCopyMaxOpen {
		t.Fatalf("copy pool: MaxOpenConnections = %d, want %d", got, nodeCopyMaxOpen)
	}
}
