package main

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The home keeps a machine's word on its processes' certificates on the home's database
// in its cluster_nodes row's update_state (db_client_cert), where the bundled Postgres's
// pg_hba.conf reads it; the latest word wins, and the rest of update_state stays.
func TestRecordDBClientCertKeepsTheMachinesWord(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	ctx := context.Background()
	id := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, update_state)
	    VALUES ($1, 'm', 'approved', 'windows', 'amd64', 'cHVi', '{"kept": 1}')`, id); err != nil {
		t.Fatal(err)
	}
	hl := &homeLink{db: db}
	word := func() (string, string) {
		t.Helper()
		var said, kept string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(update_state->>'db_client_cert', ''), COALESCE(update_state->>'kept', '')
		    FROM cluster_nodes WHERE id = $1`, id).Scan(&said, &kept); err != nil {
			t.Fatal(err)
		}
		return said, kept
	}
	if err := hl.RecordDBClientCert(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if said, kept := word(); said != "true" || kept != "1" {
		t.Fatalf("kept %q (the rest %q)", said, kept)
	}
	if err := hl.RecordDBClientCert(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if said, _ := word(); said != "false" {
		t.Fatalf("the latest word: %q", said)
	}
}
