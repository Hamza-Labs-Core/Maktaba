package main

import (
	"io"
	"log/slog"
	"testing"
)

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
