//go:build unix

package main

// 21: the first update to a release that never puts the backup back by itself, under what
// was there before it. The previous release's own binaries, as it shipped them, meet this
// commit's record: its launcher's guard (which marks a record "restoring" when it gives up on
// a release), and its server (whose database step restores a record so marked, unasked, and
// whose updater marks it too when it comes back by itself). Both leave a record with no dump
// alone, and this commit's withholds its update's until the watch passes
// (apppayload.Database.Unconfirmed).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestUpdateRehearsal21OldLauncher: the home on the previous release updates to this commit,
// whose server is killed each time it starts once its migrations ran (a crash loop). The
// previous release's launcher runs it all along, gives up on it and runs the previous
// release's kept server again. Nothing is put back by itself: the record is this commit's as
// it wrote it, the previous release stops on it as older than the database, and its own
// `restore` puts the backup back when a person asks.
func TestUpdateRehearsal21OldLauncher(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	home, _ := r.homeOnPrev(profile("restore"))
	db := home.superDB("maktaba")
	before := seedCounts(t, db)
	slot := queryInt(t, db, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`)
	p := k.curSpec()
	p.launcher = "prev"
	r.publish(p.version, home.offer(p))
	states := home.watchStates()
	owner := home.signIn()
	owner.updateNow(p.version)
	var rec dbRecord
	var all map[string]any
	waitFor(t, "this commit's migrations", 20*time.Minute, func() bool {
		rec, all = home.record()
		return layoutVersion(rec.Version) == layoutVersion(k.cur) && rec.State == "migrated"
	})
	backup, _ := all["dump_unconfirmed"].(string)
	if rec.Dump != "" || backup == "" {
		t.Fatalf("the record does not withhold its backup: %v", all)
	}
	// the crash loop: its server ends whenever it runs, until the guard gives up on it
	waitFor(t, "the previous release's launcher giving up on it", 10*time.Minute, func() bool {
		_ = exec.Command("pkill", "-9", "-f", filepath.Join(home.payload, "bin", "maktaba-server")+" serve").Run()
		return strings.TrimSpace(home.readFile(filepath.Join("updates", "payload-override"))) != ""
	})
	waitFor(t, "the previous release stopped on the database", 5*time.Minute, func() bool {
		return strings.Contains(home.log.String(), "is older than the one whose changes the library database holds")
	})
	time.Sleep(30 * time.Second) // what it would put back by itself, it would by now
	states.end()
	if home.ready() {
		t.Error("the API serves on a database newer than its release")
	}
	for _, s := range states.recordStates() {
		if s == "restoring" || s == "restored" {
			t.Errorf("a restore ran by itself: the database's record went %v", states.recordStates())
			break
		}
	}
	now, allNow := home.record()
	if layoutVersion(now.Version) != layoutVersion(k.cur) || now.State != "migrated" || allNow["dump_unconfirmed"] != backup {
		t.Errorf("the record changed: %v", allNow)
	}
	if strings.Contains(home.log.String(), "restoring the whole dump") {
		t.Error("the whole dump was put back by itself")
	}
	if n := queryInt(t, db, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`); n != int64(k.rev+1) {
		t.Errorf("the schema is at %d, want this commit's %d", n, k.rev+1)
	}
	r.countsUnchanged(before, seedCounts(t, db))
	// a person asks: the previous release's own command puts it back
	home.restoreByHand(filepath.Base(backup))
	home.waitServing(k.prev, 10*time.Minute)
	db = home.superDB("maktaba")
	if n := queryInt(t, db, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`); n != slot {
		t.Errorf("the schema is at %d after the restore, want %d", n, slot)
	}
	r.countsUnchanged(before, seedCounts(t, db))
}

// TestUpdateRehearsal21OldLauncherKinds: the previous release's launcher, with a server that
// ends as soon as it starts, in each kind that keeps its launcher across an update (a
// side-by-side layout, a runtime as the container's and the Linux app's) and in an app's
// payload: it goes back to the release before, and leaves this commit's record byte for byte
// as it was. As a check that its guard is what is tried, the record a release before this
// one wrote (its dump named) is marked restoring.
func TestUpdateRehearsal21OldLauncherKinds(t *testing.T) {
	// MAKTABA_OLD_LAUNCHER: a released launcher to try by hand, with no kit
	launcher := os.Getenv("MAKTABA_OLD_LAUNCHER")
	if launcher == "" {
		launcher = filepath.Join(newRehearsal(t).kit.dir, "prev", "maktaba-launcher")
	}
	const stub = "#!/bin/sh\nd=$(cd \"$(dirname \"$0\")\" && pwd -P)\necho \"$d\" >> \"$MAKTABA_STUB_TRACE\"\n" +
		"case \"$d\" in */1.0.0*) exec sleep 600;; esac\nexit 1\n"
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	payloadJSON := func(v string) string {
		return fmt.Sprintf(`{"schema":1,"version":%q,"platform":%q}`, v, feedPlatformKey())
	}
	const withheld = `{"version":"2.0.0","state":"migrated","from":"1.0.0","dump_unconfirmed":"/b/pre-1.0.0-to-2.0.0.dump"}`
	const named = `{"version":"2.0.0","state":"migrated","from":"1.0.0","dump":"/b/pre-1.0.0-to-2.0.0.dump"}`
	guard := func(root, record string, more map[string]any) {
		g := map[string]any{"version": "2.0.0", "run_id": "run-1", "since": time.Now().UTC(), "record": record}
		for key, v := range more {
			g[key] = v
		}
		raw, _ := json.Marshal(g)
		write(filepath.Join(root, "updates", "pending.json"), string(raw), 0o600)
	}
	kinds := map[string]func(t *testing.T, root, record string) *exec.Cmd{
		"app": func(_ *testing.T, root, record string) *exec.Cmd {
			bundle, kept := filepath.Join(root, "Maktaba.app", "server"), filepath.Join(root, "updates", "payloads", "1.0.0")
			for dir, v := range map[string]string{bundle: "2.0.0", kept: "1.0.0"} {
				write(filepath.Join(dir, "bin", "maktaba-server"), stub, 0o755)
				write(filepath.Join(dir, "payload.json"), payloadJSON(v), 0o644)
			}
			guard(root, record, map[string]any{"previous_payload": kept})
			return exec.Command(launcher, "--app-bundle", bundle, "--root", root, "serve")
		},
		"runtime": func(t *testing.T, root, record string) *exec.Cmd {
			runtimes := filepath.Join(root, "runtime")
			for _, v := range []string{"1.0.0", "2.0.0"} {
				write(filepath.Join(runtimes, v, "bin", "maktaba-server"), stub, 0o755)
				write(filepath.Join(runtimes, v, "payload.json"), payloadJSON(v), 0o644)
				if err := copyFileMode(launcher, filepath.Join(runtimes, v, "bin", "maktaba-launcher"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("2.0.0", filepath.Join(runtimes, "current")); err != nil {
				t.Fatal(err)
			}
			guard(root, record, map[string]any{"previous_runtime": "1.0.0"})
			return exec.Command(filepath.Join(runtimes, "2.0.0", "bin", "maktaba-launcher"), "--root", root, "serve")
		},
		"layout": func(_ *testing.T, root, record string) *exec.Cmd {
			sum := sha256.Sum256([]byte(stub))
			for _, v := range []string{"1.0.0", "2.0.0"} {
				dir := filepath.Join(root, "bin", "releases", v)
				write(filepath.Join(dir, "maktaba-server"), stub, 0o755)
				write(filepath.Join(dir, ".verified"), fmt.Sprintf(`{"sha256":{"maktaba-server":%q},"verified_at":%q,"identity":"migrated","version":%q}`,
					hex.EncodeToString(sum[:]), time.Now().UTC().Format(time.RFC3339), v), 0o600)
			}
			write(filepath.Join(root, "current"), "2.0.0\n", 0o600)
			write(filepath.Join(root, "previous"), "1.0.0\n", 0o600)
			pending, _ := json.Marshal(map[string]any{"version": "2.0.0", "previous": "1.0.0", "run_id": "run-1",
				"since": time.Now().UTC(), "record": record})
			write(filepath.Join(root, "pending"), string(pending), 0o600)
			return exec.Command(launcher, "--root", root, "serve")
		},
	}
	for name, setUp := range kinds {
		for what, body := range map[string]string{"withheld": withheld, "named": named} {
			t.Run(name+"/"+what, func(t *testing.T) {
				root := t.TempDir()
				record := filepath.Join(root, "state", "database.json")
				write(record, body, 0o644)
				trace := filepath.Join(root, "trace")
				cmd := setUp(t, root, record)
				var log strings.Builder
				cmd.Env = append(os.Environ(), "HOME="+root, "MAKTABA_STUB_TRACE="+trace, "MAKTABA_INSTALL_ROOT=")
				cmd.Stdout, cmd.Stderr = &log, &log
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				go func() { _ = cmd.Wait(); close(done) }()
				defer func() {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					<-done
				}()
				waitFor(t, "the release before running again", 3*time.Minute, func() bool {
					b, _ := os.ReadFile(trace)
					return strings.Contains(string(b), "1.0.0")
				})
				got, err := os.ReadFile(record)
				if err != nil {
					t.Fatal(err)
				}
				if what == "withheld" && string(got) != body {
					t.Errorf("the previous release's launcher changed this commit's record:\n%s", got)
				}
				var rec dbRecord
				if what == "named" && (json.Unmarshal(got, &rec) != nil || rec.State != "restoring") {
					t.Errorf("its guard did not act on a record with its dump named (the test shows nothing):\n%s", got)
				}
			})
		}
	}
}
