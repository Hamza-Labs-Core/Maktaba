package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
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

	t.Run("untouched pending restore matching old system id is reconciled", func(t *testing.T) {
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
		seedClusterRecord(root, "16", "18", "111111", "222222", &log)
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

	t.Run("mismatched old system id is preserved and still detected as replaced", func(t *testing.T) {
		root := t.TempDir()
		writeOld(t, root, apppayload.Database{
			Version:   "v0.4.0",
			State:     apppayload.DatabaseRestoring,
			From:      "v0.3.0",
			By:        "admin",
			Dump:      "/backups/pre-v0.3.0-to-v0.4.0.dump",
			Data:      apppayload.DataUntouched,
			DataID:    "999999/16384/2200",
			DataTable: "16400",
		})
		var log bytes.Buffer
		seedClusterRecord(root, "16", "18", "111111", "222222", &log)
		got := readNew(t, root)
		if got.DataID != "999999/16384/2200" {
			t.Fatalf("mismatched DataID was cleared: %+v", got)
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

	t.Run("already replaced or inside-marked record is never cleared", func(t *testing.T) {
		for name, rec := range map[string]apppayload.Database{
			"replaced": {
				Version: "v0.4.0", State: apppayload.DatabaseRestoring, From: "v0.3.0", By: "admin",
				Dump: "/b/d.dump", Data: apppayload.DataReplaced, DataID: "111111/16384/2200",
			},
			"inside-mark": {
				Version: "v0.4.0", State: apppayload.DatabaseRestoring, From: "v0.3.0", By: "admin",
				Dump: "/b/d.dump", Data: apppayload.DataUntouched, DataID: "111111/16384/2200" + insideMark,
			},
		} {
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				writeOld(t, root, rec)
				seedClusterRecord(root, "16", "18", "111111", "222222", &bytes.Buffer{})
				got := readNew(t, root)
				if got.DataID != rec.DataID {
					t.Fatalf("DataID changed: %+v, want %q", got, rec.DataID)
				}
			})
		}
	})
}
