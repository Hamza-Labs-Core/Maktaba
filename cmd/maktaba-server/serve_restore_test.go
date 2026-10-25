package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/attempts"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbsafe"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
)

// keepInstaller is an install that keeps releases (updater.Keeper) for the restore's
// tests: the rest of an installer is a flat install's.
type keepInstaller struct {
	flatInstaller
	running string
	kept    []updater.KeptRelease
	ran     []string
	runErr  error
}

func (k *keepInstaller) Running() string             { return k.running }
func (k *keepInstaller) Kept() []updater.KeptRelease { return k.kept }
func (k *keepInstaller) RunKept(_ context.Context, r updater.KeptRelease) error {
	if k.runErr != nil {
		return k.runErr
	}
	k.ran = append(k.ran, r.Version)
	return nil
}

// restoreRig is a home with backups, kept releases and a dump schema reader.
type restoreRig struct {
	r       *restorer
	inst    *keepInstaller
	backups string
	exits   chan int
	run     *updater.Journal
}

func newRestoreRig(t *testing.T, schemas map[string]int64) *restoreRig {
	t.Helper()
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	for name := range schemas {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("dump"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inst := &keepInstaller{running: "0.3.0", kept: []updater.KeptRelease{
		{Version: "0.2.0", Schema: 140}, {Version: "0.2.1", Schema: 140}, {Version: "0.3.0", Schema: 149, Running: true}}}
	rig := &restoreRig{inst: inst, backups: backups, exits: make(chan int, 1)}
	rig.r = &restorer{stateDir: filepath.Join(dir, "state"), db: &dbsafe.Target{Backups: backups}, inst: inst,
		run: func() *updater.Journal { return rig.run }, exit: func(c int) { rig.exits <- c }, log: io.Discard,
		schema: func(_ context.Context, p string) (int64, error) {
			s, ok := schemas[filepath.Base(p)]
			if !ok || s < 0 {
				return 0, errors.New("pg_restore: input file does not appear to be a valid archive")
			}
			return s, nil
		}}
	return rig
}

// TC7: a backup runs on the kept release whose schema is its own, the release it was
// taken from first, else the newest; none kept: the release to install is named (EC4).
func TestRestoreChoosesMatchingRelease(t *testing.T) {
	kept := []updater.KeptRelease{{Version: "0.2.0", Schema: 140}, {Version: "0.2.1", Schema: 140},
		{Version: "0.3.0", Schema: 149, Running: true}, {Version: "0.1.0"}}
	for _, c := range []struct {
		schema int64
		hint   string
		want   string
	}{
		{140, "", "0.2.1"},       // the newest of that schema
		{140, "v0.2.0", "0.2.0"}, // the one it was taken from
		{149, "0.2.1", "0.3.0"},  // the hint's schema is another: by the schema
	} {
		got, err := chooseRelease(kept, c.schema, c.hint)
		if err != nil || got.Version != c.want {
			t.Errorf("schema %d hint %q: %+v %v, want %s", c.schema, c.hint, got, err, c.want)
		}
	}
	_, err := chooseRelease(kept, 130, "0.1.5")
	if restoreCode(err) != "restore_no_release" || !strings.Contains(err.Error(), "install 0.1.5") ||
		!strings.Contains(err.Error(), "0.2.0 (0140)") {
		t.Fatalf("none kept: %v", err)
	}
	if _, err := chooseRelease(kept, 0, ""); restoreCode(err) != "restore_no_release" {
		t.Fatalf("a release of unknown schema was taken for a dump of none: %v", err)
	}
	rig := newRestoreRig(t, map[string]int64{"pre-v0.2.0-to-v0.3.0.dump": 140})
	pl, err := rig.r.plan(context.Background(), "pre-v0.2.0-to-v0.3.0.dump")
	if err != nil || pl.Release != "0.2.0" || pl.Schema != 140 || pl.Running != "0.3.0" || pl.At.IsZero() {
		t.Fatalf("plan %+v %v", pl, err)
	}
	if len(rig.inst.ran) != 0 {
		t.Fatal("a plan changed something")
	}
}

// TC7: nothing happens without the confirmation, nor for a name that is no backup here, a
// dump that cannot be read, or while an update runs.
func TestRestoreRefusesWithoutConfirm(t *testing.T) {
	rig := newRestoreRig(t, map[string]int64{"pre-v0.2.0-to-v0.3.0.dump": 140, "maktaba-20261020-0300.dump": -1})
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		confirm bool
		code    string
	}{
		{"pre-v0.2.0-to-v0.3.0.dump", false, "restore_not_confirmed"},
		{"../state/database.json", true, "restore_bad_backup"},
		{"../pre-v0.2.0-to-v0.3.0.dump", true, "restore_bad_backup"},
		{`..\pre-v0.2.0-to-v0.3.0.dump`, true, "restore_bad_backup"},
		{"..", true, "restore_bad_backup"},
		{"pre..v0.2.0-to-v0.3.0.dump", true, "restore_bad_backup"},
		{`C:\pre-v0.2.0-to-v0.3.0.dump`, true, "restore_bad_backup"},
		{"/etc/pre-v0.2.0-to-v0.3.0.dump", true, "restore_bad_backup"},
		{"notes.txt", true, "restore_bad_backup"},
		{"pre-v0.1.0-to-v0.2.0.dump", true, "restore_no_backup"},
		{"maktaba-20261020-0300.dump", true, "restore_unreadable"},
	} {
		if _, err := rig.r.restore(ctx, c.name, "root", c.confirm); restoreCode(err) != c.code {
			t.Errorf("%s (confirm %v): %v, want %s", c.name, c.confirm, err, c.code)
		}
	}
	rig.run = &updater.Journal{Job: updater.Job{Version: "0.4.0"}, State: updater.StateWatching}
	if _, err := rig.r.restore(ctx, "pre-v0.2.0-to-v0.3.0.dump", "root", true); restoreCode(err) != "restore_update_under_way" {
		t.Fatalf("during a run: %v", err)
	}
	rig.run.State = updater.StateCommitted
	noDB := *rig.r
	noDB.db = nil
	if _, err := noDB.restore(ctx, "pre-v0.2.0-to-v0.3.0.dump", "root", true); restoreCode(err) != "restore_no_database" {
		t.Fatalf("a home without a Postgres of its own: %v", err)
	}
	if len(rig.inst.ran) != 0 || len(rig.exits) != 0 {
		t.Fatalf("a refused restore changed something: ran %v", rig.inst.ran)
	}
	if _, ok, _ := apppayload.ReadDatabase(rig.r.stateDir); ok {
		t.Fatal("a refused restore wrote the record")
	}
}

// A confirmed restore marks the record (the dump, the release it is put back for, who
// asked), points the install at that release and restarts the home; a switch that fails
// leaves the record as it was and restarts nothing.
func TestRestoreMarksTheRecordAndRestarts(t *testing.T) {
	rig := newRestoreRig(t, map[string]int64{"pre-v0.2.0-to-v0.3.0.dump": 140})
	if err := apppayload.WriteDatabase(rig.r.stateDir, apppayload.Database{Version: "0.3.0",
		State: apppayload.DatabaseMigrated, From: "0.2.0", Dump: "/old.dump"}); err != nil {
		t.Fatal(err)
	}
	rig.inst.runErr = errors.New("0.2.0 is not a valid release")
	if _, err := rig.r.restore(context.Background(), "pre-v0.2.0-to-v0.3.0.dump", "root", true); restoreCode(err) != "restore_switch_failed" {
		t.Fatalf("a failed switch: %v", err)
	}
	if rec, _, _ := apppayload.ReadDatabase(rig.r.stateDir); rec.State != apppayload.DatabaseMigrated || rec.Dump != "/old.dump" {
		t.Fatalf("the record after a failed switch: %+v", rec)
	}
	rig.inst.runErr = nil
	pl, err := rig.r.restore(context.Background(), "pre-v0.2.0-to-v0.3.0.dump", "root", true)
	if err != nil || pl.Release != "0.2.0" {
		t.Fatalf("restore %+v %v", pl, err)
	}
	rec, _, _ := apppayload.ReadDatabase(rig.r.stateDir)
	if rec.State != apppayload.DatabaseRestoring || rec.Version != "0.3.0" || rec.From != "0.2.0" || rec.By != "root" ||
		rec.Failed != "" || rec.Dump != filepath.Join(rig.backups, "pre-v0.2.0-to-v0.3.0.dump") {
		t.Fatalf("record %+v", rec)
	}
	if len(rig.inst.ran) != 1 || rig.inst.ran[0] != "0.2.0" {
		t.Fatalf("ran %v", rig.inst.ran)
	}
	select {
	case c := <-rig.exits:
		if c != 75 {
			t.Fatalf("exit %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the home did not restart")
	}
	// asked again before the start put it back: refused, nothing marked twice
	if _, err := rig.r.restore(context.Background(), "pre-v0.2.0-to-v0.3.0.dump", "root", true); restoreCode(err) != "restore_under_way" {
		t.Fatalf("a second restore: %v", err)
	}
}

// The release a restore points at puts the backup back at its start, before any role:
// the database it replaces set aside, the record restored for that release, no update
// attempt (nothing failed), and the start goes on; on the running release too.
func TestRestoreAskedPutsTheBackupBackBeforeAnyRole(t *testing.T) {
	for _, c := range []struct{ name, payload, held string }{
		{"an older kept release", "v0.2.0", "v0.3.0"},
		{"the running release", "v0.3.0", "v0.3.0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(envLauncherRoot, "")
			cfg, payload := appEnv(t, c.payload, "")
			backups := filepath.Join(cfg.StateDir(), "backups")
			_ = os.MkdirAll(backups, 0o700)
			dump := filepath.Join(backups, "maktaba-20261020-0300.dump")
			_ = os.WriteFile(dump, []byte("dump"), 0o600)
			record(t, cfg, apppayload.Database{Version: c.held, State: apppayload.DatabaseRestoring, From: c.payload,
				Dump: dump, By: "root", Error: "restore asked by root"})
			f := installFakeDB(t, cfg)
			var shown []string
			ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
			err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), func(d supervisor.Detail) {
				shown = append(shown, d.Code)
			})
			if err != nil {
				t.Fatalf("the start = %v", err)
			}
			if len(f.restores) != 1 || f.restores[0] != filepath.Base(dump) {
				t.Fatalf("restores %v", f.restores)
			}
			if len(f.asides) != 1 || !strings.HasPrefix(f.asides[0], "replaced-"+c.held+"-") {
				t.Fatalf("asides %v", f.asides)
			}
			if !strings.Contains(strings.Join(shown, " "), "db_restoring_backup") {
				t.Fatalf("details %v", shown)
			}
			if rec := recorded(t, cfg); rec.State != apppayload.DatabaseMigrated || rec.Version != c.payload {
				t.Fatalf("record %+v", rec)
			}
			if got := attempts.Read(filepath.Join(cfg.StateDir(), "updates")); len(got) != 0 {
				t.Fatalf("a restore by hand recorded update attempts: %+v", got)
			}
		})
	}
}

// The Updates page's list: every dump of the backups folder by kind (the releases of an
// update's), newest first, nothing else; the kept releases; a restore under way.
func TestRestoreListsTheBackups(t *testing.T) {
	rig := newRestoreRig(t, map[string]int64{"pre-v0.2.0-to-v0.3.0.dump": 140, "failed-v0.3.0-20261021T030000.dump": 149,
		"replaced-v0.3.0-20261022T100000.dump": 140, "maktaba-2026-10-23_0300.dump": 149, "pre-unknown-to-v0.1.0.dump": 1})
	_ = os.WriteFile(filepath.Join(rig.backups, "backups-keep"), []byte("3"), 0o600)
	_ = os.Mkdir(filepath.Join(rig.backups, ".rehearse-abc"), 0o700)
	at := time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)
	for i, n := range []string{"pre-unknown-to-v0.1.0.dump", "pre-v0.2.0-to-v0.3.0.dump", "failed-v0.3.0-20261021T030000.dump",
		"replaced-v0.3.0-20261022T100000.dump", "maktaba-2026-10-23_0300.dump"} {
		ts := at.Add(time.Duration(i) * 24 * time.Hour)
		_ = os.Chtimes(filepath.Join(rig.backups, n), ts, ts)
	}
	l := rig.r.list()
	var kinds []string
	for _, b := range l.Backups {
		kinds = append(kinds, b.Kind)
	}
	if strings.Join(kinds, ",") != "daily,replaced,failed,update,update" {
		t.Fatalf("kinds %v", kinds)
	}
	if u := l.Backups[3]; u.From != "0.2.0" || u.To != "0.3.0" || u.Bytes != 4 {
		t.Fatalf("an update's backup %+v", u)
	}
	if u := l.Backups[4]; u.From != "" || u.To != "0.1.0" {
		t.Fatalf("a backup from an unknown release %+v", u)
	}
	if len(l.Kept) != 3 || l.Running != "0.3.0" || l.Restoring != "" {
		t.Fatalf("list %+v", l)
	}
	_ = apppayload.WriteDatabase(rig.r.stateDir, apppayload.Database{State: apppayload.DatabaseRestoring,
		Dump: filepath.Join(rig.backups, "pre-v0.2.0-to-v0.3.0.dump")})
	if l := rig.r.list(); l.Restoring != "pre-v0.2.0-to-v0.3.0.dump" {
		t.Fatalf("restoring %q", l.Restoring)
	}
}

// The supervisor's loopback admin: the list, a plan, a restore (a refusal says its code);
// a node keeps no backups of its own.
func TestRestoreEndpoints(t *testing.T) {
	rig := newRestoreRig(t, map[string]int64{"pre-v0.2.0-to-v0.3.0.dump": 140})
	u := &updates{m: &updater.Machine{Dir: t.TempDir(), Installer: rig.inst}, r: rig.r}
	h := u.adminHandler(http.NotFoundHandler(), "tok")
	call := func(method, path, body string) (int, map[string]json.RawMessage) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var doc map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &doc)
		return rec.Code, doc
	}
	if code, doc := call(http.MethodGet, "/update/backups", ""); code != http.StatusOK || !strings.Contains(string(doc["backups"]), "pre-v0.2.0-to-v0.3.0.dump") {
		t.Fatalf("list %d %s", code, doc["backups"])
	}
	if code, doc := call(http.MethodGet, "/update/restore?backup=pre-v0.2.0-to-v0.3.0.dump", ""); code != http.StatusOK ||
		!strings.Contains(string(doc["plan"]), `"release":"0.2.0"`) {
		t.Fatalf("plan %d %s", code, doc["plan"])
	}
	if code, doc := call(http.MethodPost, "/update/restore", `{"backup":"pre-v0.2.0-to-v0.3.0.dump","by":"root"}`); code != http.StatusConflict ||
		string(doc["code"]) != `"restore_not_confirmed"` {
		t.Fatalf("unconfirmed %d %s", code, doc["code"])
	}
	if code, _ := call(http.MethodPost, "/update/restore", `{"backup":"pre-v0.2.0-to-v0.3.0.dump","by":"root","confirm":true}`); code != http.StatusAccepted {
		t.Fatalf("restore %d", code)
	}
	req := httptest.NewRequest(http.MethodGet, "/update/backups", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("without the token: %d", rec.Code)
	}
	node := (&updates{m: u.m}).adminHandler(http.NotFoundHandler(), "tok")
	req = httptest.NewRequest(http.MethodGet, "/update/backups", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	node.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "restore_not_home") {
		t.Fatalf("a node: %d %s", rec.Code, rec.Body.String())
	}
}
