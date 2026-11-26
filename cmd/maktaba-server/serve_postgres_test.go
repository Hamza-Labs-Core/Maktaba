package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// A home on a Postgres of its own is told at its start that its machines keep no copy of
// it without the rule, and what the rule would expose; a home on SQLite is told nothing.
func TestAHomeOnItsOwnPostgresIsWarned(t *testing.T) {
	var out strings.Builder
	cfg := config.Config{}
	cfg.Database.URL = "postgres://maktaba@db.example:5432/maktaba?sslmode=disable"
	warnOperatorPostgres(&out, cfg)
	for _, want := range []string{"rules.cluster.copies_on_operator_postgres", "test_decoding", "pg_signal_backend",
		"Remove those plugins"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("the warning does not say %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	cfg.Database.URL = "sqlite:///var/lib/maktaba/maktaba.db"
	warnOperatorPostgres(&out, cfg)
	if out.Len() != 0 {
		t.Fatalf("a home on SQLite was warned:\n%s", out.String())
	}
}

func writeOldClusterRecord(t *testing.T, root string, rec apppayload.Database) {
	t.Helper()
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, apppayload.ClusterRecordName("16")), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readNewClusterRecord(t *testing.T, root string) apppayload.Database {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "state", apppayload.ClusterRecordName("18")))
	if err != nil {
		t.Fatal(err)
	}
	var rec apppayload.Database
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// PG 16->18 upgrade changes the cluster's system identifier (and may change database OID):
// seeding the new cluster's record reconciles a verified untouched pending restore's old
// DataID so settleData on PG 18 keeps DataUntouched and pins the new identity, instead of
// falsely classifying it as replaced. Mismatched, already-replaced, or inside-marked records
// are never cleared.
func TestSeedClusterRecordReconcilesUntouchedPendingRestoreIdentity(t *testing.T) {
	writeOld, readNew := writeOldClusterRecord, readNewClusterRecord

	t.Run("untouched pending restore matching full old identity is reconciled", func(t *testing.T) {
		root := t.TempDir()
		writeOld(t, root, apppayload.Database{
			Version:   "v0.4.0",
			State:     apppayload.DatabaseRestoring,
			From:      "v0.3.0",
			By:        "admin",
			Dump:      "/backups/pre-v0.3.0-to-v0.4.0.dump",
			Data:      apppayload.DataUntouched,
			DataID:    "111111/16384/2200",
			DataTable: "16400",
		})
		var log bytes.Buffer
		if err := seedClusterRecord(root, "16", "18", "111111/16384/2200", "16400", "222222", &log); err != nil {
			t.Fatal(err)
		}
		got := readNew(t, root)
		if got.Data != apppayload.DataUntouched || got.DataID != "" || got.DataTable != "" {
			t.Fatalf("seeded record = %+v, want DataUntouched with cleared DataID/DataTable", got)
		}
		// Next settleData on the upgraded PG 18 cluster adopts PG 18's identity as untouched.
		settled, err := settleData(context.Background(), got, dataProbe{
			identity: func(context.Context) (string, int64, error) { return "222222/16385/2200#16400", 150, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		if settled.Data != apppayload.DataUntouched || settled.DataID != "222222/16385/2200" || settled.DataTable != "16400" {
			t.Fatalf("settled on PG 18 = %+v", settled)
		}
	})

	t.Run("same cluster system id but changed db, schema, or migration table is not cleared", func(t *testing.T) {
		cases := map[string]struct {
			oldID, oldTable string
		}{
			"changed-db":     {"111111/16399/2200", "16400"},
			"changed-schema": {"111111/16384/2299", "16400"},
			"changed-table":  {"111111/16384/2200", "16999"},
			"missing-table":  {"111111/16384/2200", ""},
			"missing-old-id": {"", "16400"},
			"other-cluster":  {"999999/16384/2200", "16400"},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				writeOld(t, root, apppayload.Database{
					Version:   "v0.4.0",
					State:     apppayload.DatabaseRestoring,
					From:      "v0.3.0",
					By:        "admin",
					Dump:      "/backups/pre-v0.3.0-to-v0.4.0.dump",
					Data:      apppayload.DataUntouched,
					DataID:    "111111/16384/2200",
					DataTable: "16400",
				})
				var log bytes.Buffer
				if err := seedClusterRecord(root, "16", "18", c.oldID, c.oldTable, "222222", &log); err != nil {
					t.Fatal(err)
				}
				got := readNew(t, root)
				if got.DataID != "111111/16384/2200" || got.DataTable != "16400" {
					t.Fatalf("mismatched identity was cleared: %+v", got)
				}
				settled, err := settleData(context.Background(), got, dataProbe{
					identity: func(context.Context) (string, int64, error) { return "222222/16385/2200#16400", 150, nil },
				})
				if err != nil {
					t.Fatal(err)
				}
				if settled.Data != apppayload.DataReplaced {
					t.Fatalf("settled = %+v, want DataReplaced", settled)
				}
			})
		}
	})

	t.Run("already replaced or inside-marked record is never cleared", func(t *testing.T) {
		for name, rec := range map[string]apppayload.Database{
			"replaced": {
				Version: "v0.4.0", State: apppayload.DatabaseRestoring, From: "v0.3.0", By: "admin",
				Dump: "/b/d.dump", Data: apppayload.DataReplaced, DataID: "111111/16384/2200", DataTable: "16400",
			},
			"inside-mark": {
				Version: "v0.4.0", State: apppayload.DatabaseRestoring, From: "v0.3.0", By: "admin",
				Dump: "/b/d.dump", Data: apppayload.DataUntouched, DataID: "111111/16384/2200" + insideMark, DataTable: "16400",
			},
		} {
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				writeOld(t, root, rec)
				if err := seedClusterRecord(root, "16", "18", "111111/16384/2200", "16400", "222222", &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
				got := readNew(t, root)
				if got.DataID != rec.DataID {
					t.Fatalf("DataID changed: %+v, want %q", got, rec.DataID)
				}
			})
		}
	})
}

// seedClusterRecordIfPending seeds PG 18's record only when RecordSeedPending is present
// and the source PG 16 cluster still matches UpgradeMarker, leaving legacy pre-PR PG 18
// homes and replaced-source cases untouched while failing closed on I/O or control errors.
func TestSeedClusterRecordIfPending(t *testing.T) {
	writeOld, readNew := writeOldClusterRecord, readNewClusterRecord
	t.Run("legacy pre-PR PG 18 home without pending marker keeps live database-pg18.json untouched", func(t *testing.T) {
		for _, withOld := range []bool{true, false} {
			name := "without-old-record"
			if withOld {
				name = "with-old-record"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				stateDir := filepath.Join(root, "state")
				if err := os.MkdirAll(stateDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if withOld {
					writeOld(t, root, apppayload.Database{
						Version: "v0.3.0", State: apppayload.DatabaseMigrated,
					})
				}
				livePG18 := apppayload.Database{
					Version: "v0.4.0", State: apppayload.DatabaseRestoring, From: "v0.3.9",
					By: "admin", Dump: "/backups/live-pg18.dump", Data: apppayload.DataUntouched, DataID: "222222/16385/2200",
				}
				liveRaw, _ := json.MarshalIndent(livePG18, "", "  ")
				if err := os.WriteFile(filepath.Join(stateDir, apppayload.ClusterRecordName("18")), append(liveRaw, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
				pg18Data := filepath.Join(root, "postgres-18")
				if err := os.MkdirAll(pg18Data, 0o700); err != nil {
					t.Fatal(err)
				}
				markerRaw, _ := json.Marshal(pgbundle.UpgradedFrom{
					Major: "16", SystemID: "111111", Checkpoint: "0/1",
				})
				if err := os.WriteFile(filepath.Join(pg18Data, pgbundle.UpgradeMarker), markerRaw, 0o600); err != nil {
					t.Fatal(err)
				}
				ob := pgbundle.Bundle{Home: filepath.Join(root, "pg16"), Data: filepath.Join(root, "postgres")}
				b := pgbundle.Bundle{Home: filepath.Join(root, "pg18"), Data: pg18Data}
				var log bytes.Buffer
				if err := seedClusterRecordIfPending(context.Background(), root, ob, b, "16", "18", &log); err != nil {
					t.Fatalf("legacy PG 18 home returned error: %v", err)
				}
				got := readNew(t, root)
				if got.Dump != livePG18.Dump || got.DataID != livePG18.DataID || got.State != livePG18.State {
					t.Fatalf("legacy PG 18 record was modified: got=%+v want=%+v", got, livePG18)
				}
			})
		}
	})

	makeFakeCluster := func(t *testing.T, root, homeName, dataName, major, sysID, ckpt string) pgbundle.Bundle {
		t.Helper()
		home := filepath.Join(root, homeName)
		data := filepath.Join(root, dataName)
		if err := os.MkdirAll(data, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "PG_VERSION"), []byte(major+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		ctrl := "#!/bin/sh\necho 'Database cluster state: shut down'\necho 'Database system identifier: " + sysID + "'\necho 'Latest checkpoint location: " + ckpt + "'\n"
		if err := os.WriteFile(filepath.Join(home, "bin", "pg_controldata"), []byte(ctrl), 0o755); err != nil {
			t.Fatal(err)
		}
		return pgbundle.Bundle{Home: home, Data: data}
	}

	t.Run("crash after writing RecordSeedPending and UpgradeMarker retries cleanly and removes pending marker", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("shell stand-in for pg_controldata")
		}
		root := t.TempDir()
		writeOld(t, root, apppayload.Database{
			Version:   "v0.4.0",
			State:     apppayload.DatabaseRestoring,
			From:      "v0.3.0",
			By:        "admin",
			Dump:      "/backups/pre-v0.3.0-to-v0.4.0.dump",
			Data:      apppayload.DataUntouched,
			DataID:    "111111/16384/2200",
			DataTable: "16400",
		})
		ob := makeFakeCluster(t, root, "pg16", "postgres", "16", "111111", "0/1")
		b := makeFakeCluster(t, root, "pg18", "postgres-18", "18", "222222", "0/2")
		// Simulate crash right after MajorUpgrade wrote RecordSeedPending + UpgradeMarker.
		pendingPath := filepath.Join(b.Data, pgbundle.RecordSeedPending)
		if err := os.WriteFile(pendingPath, []byte("111111@0/1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		markerRaw, _ := json.Marshal(pgbundle.UpgradedFrom{
			Major: "16", SystemID: "111111", Checkpoint: "0/1", Identity: "111111/16384/2200", Table: "16400",
		})
		if err := os.WriteFile(filepath.Join(b.Data, pgbundle.UpgradeMarker), markerRaw, 0o600); err != nil {
			t.Fatal(err)
		}
		var log bytes.Buffer
		if err := seedClusterRecordIfPending(context.Background(), root, ob, b, "16", "18", &log); err != nil {
			t.Fatalf("seedClusterRecordIfPending failed: %v", err)
		}
		if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
			t.Fatalf("pending marker not removed after seeding: %v", err)
		}
		got := readNew(t, root)
		if got.Data != apppayload.DataUntouched || got.DataID != "" || got.DataTable != "" {
			t.Fatalf("after restart recovery, seeded record = %+v", got)
		}
		// Mutate the PG 18 record as live operation would, then verify a subsequent restart
		// does not overwrite it once RecordSeedPending is gone.
		got.DataID = "222222/16385/2200"
		mut, _ := json.Marshal(got)
		if err := os.WriteFile(filepath.Join(root, "state", apppayload.ClusterRecordName("18")), mut, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := seedClusterRecordIfPending(context.Background(), root, ob, b, "16", "18", &log); err != nil {
			t.Fatalf("second seedClusterRecordIfPending failed: %v", err)
		}
		after := readNew(t, root)
		if after.DataID != "222222/16385/2200" {
			t.Fatalf("subsequent restart overwrote PG 18 record: %+v", after)
		}
	})

	t.Run("replaced PG 16 cluster between crash and restart does not overwrite PG 18 record", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("shell stand-in for pg_controldata")
		}
		root := t.TempDir()
		writeOld(t, root, apppayload.Database{
			Version: "v0.1.0", State: apppayload.DatabaseMigrated,
		})
		livePG18 := apppayload.Database{
			Version: "v0.4.0", State: apppayload.DatabaseRestoring, From: "v0.3.9",
			By: "admin", Dump: "/backups/pg18-own.dump", Data: apppayload.DataUntouched, DataID: "222222/16385/2200",
		}
		liveRaw, _ := json.MarshalIndent(livePG18, "", "  ")
		if err := os.WriteFile(filepath.Join(root, "state", apppayload.ClusterRecordName("18")), append(liveRaw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		// PG 16 directory was replaced with a brand-new cluster (999999 != UpgradeMarker's 111111).
		ob := makeFakeCluster(t, root, "pg16", "postgres", "16", "999999", "0/1")
		b := makeFakeCluster(t, root, "pg18", "postgres-18", "18", "222222", "0/2")
		pendingPath := filepath.Join(b.Data, pgbundle.RecordSeedPending)
		if err := os.WriteFile(pendingPath, []byte("111111@0/1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		markerRaw, _ := json.Marshal(pgbundle.UpgradedFrom{
			Major: "16", SystemID: "111111", Checkpoint: "0/1", Identity: "111111/16384/2200", Table: "16400",
		})
		if err := os.WriteFile(filepath.Join(b.Data, pgbundle.UpgradeMarker), markerRaw, 0o600); err != nil {
			t.Fatal(err)
		}
		var log bytes.Buffer
		if err := seedClusterRecordIfPending(context.Background(), root, ob, b, "16", "18", &log); err != nil {
			t.Fatalf("replaced PG 16 returned error: %v", err)
		}
		if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
			t.Fatalf("pending marker was not cleared: %v", err)
		}
		got := readNew(t, root)
		if got.Dump != livePG18.Dump || got.DataID != livePG18.DataID || got.State != livePG18.State {
			t.Fatalf("PG 18 record was overwritten by replaced PG 16 cluster: got=%+v want=%+v", got, livePG18)
		}
	})

	t.Run("seeding or marker failure fails closed and preserves RecordSeedPending", func(t *testing.T) {
		root := t.TempDir()
		pg16Data := filepath.Join(root, "postgres")
		pg18Data := filepath.Join(root, "postgres-18")
		for _, d := range []string{pg16Data, pg18Data} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(pg16Data, "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		pendingPath := filepath.Join(pg18Data, pgbundle.RecordSeedPending)
		if err := os.WriteFile(pendingPath, []byte("111111@0/1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		markerRaw, _ := json.Marshal(pgbundle.UpgradedFrom{
			Major: "16", SystemID: "111111", Checkpoint: "0/1", Identity: "111111/16384/2200", Table: "16400",
		})
		if err := os.WriteFile(filepath.Join(pg18Data, pgbundle.UpgradeMarker), markerRaw, 0o600); err != nil {
			t.Fatal(err)
		}
		// Missing pg_controldata must fail closed and leave RecordSeedPending in place.
		ob := pgbundle.Bundle{Home: filepath.Join(root, "no-such-pg16"), Data: pg16Data}
		b := pgbundle.Bundle{Home: filepath.Join(root, "no-such-pg18"), Data: pg18Data}
		if err := seedClusterRecordIfPending(context.Background(), root, ob, b, "16", "18", &bytes.Buffer{}); err == nil {
			t.Fatal("expected error when pg_controldata fails, got nil")
		}
		if _, err := os.Stat(pendingPath); err != nil {
			t.Fatalf("pending marker was removed on failure: %v", err)
		}
		// Non-ENOENT stat error on ob.Data (regular file at ob.Data -> ENOTDIR on Unix,
		// not-a-directory on Windows) must fail closed, keep RecordSeedPending, and leave
		// PG 18's record untouched rather than treating the source cluster as gone.
		rootStat := t.TempDir()
		stateStat := filepath.Join(rootStat, "state")
		if err := os.MkdirAll(stateStat, 0o700); err != nil {
			t.Fatal(err)
		}
		livePG18 := apppayload.Database{Version: "v0.4.0", State: apppayload.DatabaseMigrated}
		liveRaw, _ := json.MarshalIndent(livePG18, "", "  ")
		if err := os.WriteFile(filepath.Join(stateStat, apppayload.ClusterRecordName("18")), append(liveRaw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		pg16File := filepath.Join(rootStat, "postgres")
		if err := os.WriteFile(pg16File, []byte("not-a-dir"), 0o600); err != nil {
			t.Fatal(err)
		}
		pg18Stat := filepath.Join(rootStat, "postgres-18")
		if err := os.MkdirAll(pg18Stat, 0o700); err != nil {
			t.Fatal(err)
		}
		pendingStat := filepath.Join(pg18Stat, pgbundle.RecordSeedPending)
		if err := os.WriteFile(pendingStat, []byte("111111@0/1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pg18Stat, pgbundle.UpgradeMarker), markerRaw, 0o600); err != nil {
			t.Fatal(err)
		}
		obStat := pgbundle.Bundle{Home: filepath.Join(rootStat, "pg16"), Data: pg16File}
		bStat := pgbundle.Bundle{Home: filepath.Join(rootStat, "pg18"), Data: pg18Stat}
		if err := seedClusterRecordIfPending(context.Background(), rootStat, obStat, bStat, "16", "18", &bytes.Buffer{}); err == nil {
			t.Fatal("expected fail-closed error on non-ENOENT/non-dir source cluster stat, got nil")
		}
		if _, err := os.Stat(pendingStat); err != nil {
			t.Fatalf("pending marker was removed on non-ENOENT source stat error: %v", err)
		}
		if got := readNew(t, rootStat); got.Version != livePG18.Version || got.State != livePG18.State {
			t.Fatalf("PG 18 record was modified on non-ENOENT source stat error: %+v", got)
		}
		// Marker removal failure (directory where pending marker file is expected) fails closed.
		if runtime.GOOS != "windows" {
			root2 := t.TempDir()
			writeOld(t, root2, apppayload.Database{Version: "v0.4.0", State: apppayload.DatabaseMigrated})
			ob2 := makeFakeCluster(t, root2, "pg16", "postgres", "16", "111111", "0/1")
			b2 := makeFakeCluster(t, root2, "pg18", "postgres-18", "18", "222222", "0/2")
			if err := os.MkdirAll(filepath.Join(b2.Data, pgbundle.RecordSeedPending, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(b2.Data, pgbundle.UpgradeMarker), markerRaw, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := seedClusterRecordIfPending(context.Background(), root2, ob2, b2, "16", "18", &bytes.Buffer{}); err == nil {
				t.Fatal("expected error when removing RecordSeedPending fails, got nil")
			}
		}
	})
}

func TestSeedKeepsRestoreRequestedOn18BeforeRollbackStarted16(t *testing.T) {
	for _, withOldRecord := range []bool{true, false} {
		root := t.TempDir()
		dir := filepath.Join(root, "state")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		to := filepath.Join(dir, apppayload.ClusterRecordName("18"))
		request := []byte(`{"state":"restoring","by":"admin","dump":"/backups/request.dump","data":"untouched","data_id":"123/456/789"}`)
		if err := os.WriteFile(to, request, 0o600); err != nil {
			t.Fatal(err)
		}
		if withOldRecord {
			if err := os.WriteFile(filepath.Join(dir, apppayload.ClusterRecordName("16")), []byte(`{"state":"migrated"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := seedClusterRecord(root, "16", "18", "", "", "321", &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(to)
		var rec apppayload.Database
		if err != nil || json.Unmarshal(got, &rec) != nil || !asked(rec) || rec.Dump != "/backups/request.dump" {
			t.Fatalf("restore request lost: %s (%v)", got, err)
		}
		settled, err := settleData(context.Background(), rec, dataProbe{
			identity: func(context.Context) (string, int64, error) { return "321/456/789#123", 150, nil },
		})
		if err != nil || settled.Data != apppayload.DataUntouched || settled.DataID != "321/456/789" {
			t.Fatalf("restored request would skip keeping the rebuilt data: %+v (%v)", settled, err)
		}
	}
}
