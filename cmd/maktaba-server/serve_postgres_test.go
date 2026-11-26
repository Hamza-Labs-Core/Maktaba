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

// PG 16->18 upgrade changes the cluster's system identifier (and may change database OID):
// seeding the new cluster's record reconciles a verified untouched pending restore's old
// DataID so settleData on PG 18 keeps DataUntouched and pins the new identity, instead of
// falsely classifying it as replaced. Mismatched, already-replaced, or inside-marked records
// are never cleared.
func TestSeedClusterRecordReconcilesUntouchedPendingRestoreIdentity(t *testing.T) {
	writeOld := func(t *testing.T, root string, rec apppayload.Database) {
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
	readNew := func(t *testing.T, root string) apppayload.Database {
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
				b := pgbundle.Bundle{Home: filepath.Join(root, "pg18"), Data: pg18Data}
				var log bytes.Buffer
				if err := seedClusterRecordIfPending(context.Background(), root, b, "16", "18", &log); err != nil {
					t.Fatalf("legacy PG 18 home returned error: %v", err)
				}
				got := readNew(t, root)
				if got.Dump != livePG18.Dump || got.DataID != livePG18.DataID || got.State != livePG18.State {
					t.Fatalf("legacy PG 18 record was modified: got=%+v want=%+v", got, livePG18)
				}
			})
		}
	})

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
		pg18Data := filepath.Join(root, "postgres-18")
		pg18Home := filepath.Join(root, "pg18")
		if err := os.MkdirAll(pg18Data, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(pg18Home, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		ctrl := "#!/bin/sh\necho 'Database cluster state: shut down'\necho 'Database system identifier: 222222'\necho 'Latest checkpoint location: 0/2'\n"
		if err := os.WriteFile(filepath.Join(pg18Home, "bin", "pg_controldata"), []byte(ctrl), 0o755); err != nil {
			t.Fatal(err)
		}
		// Simulate crash right after MajorUpgrade wrote RecordSeedPending + UpgradeMarker.
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
		b := pgbundle.Bundle{Home: pg18Home, Data: pg18Data}
		var log bytes.Buffer
		if err := seedClusterRecordIfPending(context.Background(), root, b, "16", "18", &log); err != nil {
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
		if err := seedClusterRecordIfPending(context.Background(), root, b, "16", "18", &log); err != nil {
			t.Fatalf("second seedClusterRecordIfPending failed: %v", err)
		}
		after := readNew(t, root)
		if after.DataID != "222222/16385/2200" {
			t.Fatalf("subsequent restart overwrote PG 18 record: %+v", after)
		}
	})

	t.Run("seeding or marker failure fails closed and preserves RecordSeedPending", func(t *testing.T) {
		root := t.TempDir()
		pg18Data := filepath.Join(root, "postgres-18")
		if err := os.MkdirAll(pg18Data, 0o700); err != nil {
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
		b := pgbundle.Bundle{Home: filepath.Join(root, "no-such-pg18"), Data: pg18Data}
		if err := seedClusterRecordIfPending(context.Background(), root, b, "16", "18", &bytes.Buffer{}); err == nil {
			t.Fatal("expected error when pg_controldata fails, got nil")
		}
		if _, err := os.Stat(pendingPath); err != nil {
			t.Fatalf("pending marker was removed on failure: %v", err)
		}
		// Marker removal failure (directory where pending marker file is expected) fails closed.
		if runtime.GOOS != "windows" {
			root2 := t.TempDir()
			writeOld(t, root2, apppayload.Database{Version: "v0.4.0", State: apppayload.DatabaseMigrated})
			pg18Data2 := filepath.Join(root2, "postgres-18")
			pg18Home2 := filepath.Join(root2, "pg18")
			if err := os.MkdirAll(filepath.Join(pg18Data2, pgbundle.RecordSeedPending, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(pg18Home2, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			ctrl := "#!/bin/sh\necho 'Database cluster state: shut down'\necho 'Database system identifier: 222222'\necho 'Latest checkpoint location: 0/2'\n"
			if err := os.WriteFile(filepath.Join(pg18Home2, "bin", "pg_controldata"), []byte(ctrl), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pg18Data2, pgbundle.UpgradeMarker), markerRaw, 0o600); err != nil {
				t.Fatal(err)
			}
			b2 := pgbundle.Bundle{Home: pg18Home2, Data: pg18Data2}
			if err := seedClusterRecordIfPending(context.Background(), root2, b2, "16", "18", &bytes.Buffer{}); err == nil {
				t.Fatal("expected error when removing RecordSeedPending fails, got nil")
			}
		}
	})
}
