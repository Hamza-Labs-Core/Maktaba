//go:build !windows

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// fakePostgres makes dir/bin/postgres a program that says it is PostgreSQL major.
func fakePostgres(t *testing.T, dir, major string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho 'postgres (PostgreSQL) " + major + ".7'\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "postgres"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Epic 49 story 49.2: when the upgrade cannot run (here pg_upgrade's programs are not
// there), the home stays on the previous major, on its own cluster and record, and the
// hold says why, so every command runs 16 until a start upgrades it.
func TestMajorUpgradeStepHoldsAndRunsThePreviousMajor(t *testing.T) {
	cfg, _ := appEnv(t, "v0.3.0", "")
	root := cfg.StateDir()
	payload := t.TempDir()
	old := fakePostgres(t, filepath.Join(payload, "postgres"), "16")
	nw := fakePostgres(t, filepath.Join(payload, "postgres-18"), "18")
	oldData := pgbundle.DataDirFor(root, "16")
	if err := os.MkdirAll(oldData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldData, "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := pgbundle.Bundle{Home: nw, Data: pgbundle.DataDirFor(root, "18"), Keys: t.TempDir(), Port: 1}
	var log bytes.Buffer
	got := majorUpgradeStep(context.Background(), cfg, b, &log)
	if got.Home != old || got.Data != oldData {
		t.Fatalf("after a failed upgrade it runs %s on %s, want %s on %s\n%s", got.Home, got.Data, old, oldData, log.String())
	}
	if pgbundle.HeldMajor(root) == "" {
		t.Error("the upgrade is not held")
	}
	if !strings.Contains(log.String(), "stays on PostgreSQL 16") {
		t.Errorf("the log does not say it stays on 16:\n%s", log.String())
	}
	if base := filepath.Base(apppayload.DatabasePath(root)); base != "database.json" {
		t.Errorf("the record is %s, want the 16 cluster's database.json", base)
	}
}

// A payload with one Postgres has nothing to upgrade: it runs as it is, on database.json.
func TestMajorUpgradeStepWithOneMajor(t *testing.T) {
	cfg, _ := appEnv(t, "v0.3.0", "")
	home := fakePostgres(t, filepath.Join(t.TempDir(), "postgres"), "16")
	b := pgbundle.Bundle{Home: home, Data: pgbundle.DataDirFor(cfg.StateDir(), "16")}
	if got := majorUpgradeStep(context.Background(), cfg, b, io.Discard); got.Home != home || got.Data != b.Data {
		t.Fatalf("got %s on %s", got.Home, got.Data)
	}
	if base := filepath.Base(apppayload.DatabasePath(cfg.StateDir())); base != "database.json" {
		t.Errorf("the record is %s", base)
	}
}

// The postgres role's gate compares the cluster with the Postgres this start runs, not
// payload.json's postgres (16 in an 18 release: the releases before read it): an upgraded
// 18 cluster starts.
func TestAppBundleGateTakesTheMajorItRuns(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	pg := testBundle(cfg)
	pg.Home = fakePostgres(t, filepath.Join(t.TempDir(), "postgres-18"), "18")
	pg.Data = pgbundle.DataDirFor(cfg.StateDir(), "18")
	if err := os.MkdirAll(pg.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pg.Data, "PG_VERSION"), []byte("18\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gate := prepareOf(t, appBundleGates(cfg, payload, pg, gateProcs(true), io.Discard), supervisor.RolePostgres)
	if err := gate(context.Background(), noReport); err != nil {
		t.Fatalf("an 18 cluster on the 18 it runs (payload.json says 16): %v", err)
	}
}

// Epic 49 story 49.2 (review of #1445): on a home upgraded to 18, a restore onto a release
// that ran on 16 is refused (it would start on the old cluster and never put the dump back);
// one onto a release from the upgrade on goes ahead; on a 16 home nothing is refused.
func TestRestoreRefusesAReleaseOfTheOtherMajor(t *testing.T) {
	root := t.TempDir()
	if err := sameMajorRelease(root, "0.3.140"); err != nil {
		t.Fatalf("a 16 home: %v", err)
	}
	for _, f := range []string{filepath.Join("postgres-18", "PG_VERSION"), filepath.Join("state", "database.json")} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(root, "postgres-18", "PG_VERSION"), []byte("18\n"), 0o600)
	_ = os.WriteFile(filepath.Join(root, "state", "database.json"), []byte(`{"version":"0.3.151","state":"migrated"}`), 0o600)
	for rel, refused := range map[string]bool{"0.3.140": true, "0.3.151": true, "0.3.153": false} {
		err := sameMajorRelease(root, rel)
		if (err != nil) != refused {
			t.Errorf("a restore onto %s: %v, want refused %v", rel, err, refused)
		}
		if err != nil && !strings.Contains(err.Error(), "PostgreSQL 16") {
			t.Errorf("%s: %v", rel, err)
		}
	}
}
