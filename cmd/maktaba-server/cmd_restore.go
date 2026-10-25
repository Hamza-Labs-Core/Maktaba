package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/selfupdate"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/install"
)

func newRestoreCmd() *cobra.Command {
	var (
		backup  string
		confirm bool
	)
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Put the library database back from one of the home's backups",
		Long: `restore puts the home's library database back as one of its backups holds it
(a dump in the home's backups folder: the one an update took before its
migrations, the daily one of the bundled PostgreSQL, the database a failed
update or a restore replaced). Every change made since the backup was taken is
lost; the database it replaces is set aside first.

Without --backup it lists the backups. With --backup and without --confirm it
says what the restore would do. With --confirm the home restarts on the kept
release whose schema is the backup's (one this home keeps; it refuses and names
the release to install when none is), and that release puts the backup back
before anything else runs. The Updates page does the same.

It asks the running server; with none running it prepares the restore, and the
next start of the server does it (an install with a launcher only).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			return runRestore(cmd.Context(), cmd.OutOrStdout(), cfg, backup, confirm)
		},
	}
	cmd.Flags().StringVar(&backup, "backup", "", "the backup's file name (in the home's backups folder), or its path there")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "restore it: every change since the backup was taken is lost")
	return cmd
}

// restoreAsker reaches the running supervisor's loopback admin (nil: none answers).
type restoreAsker struct {
	base, token string
	http        *http.Client
}

// supervisorAsker is the running supervisor's admin with its per-boot token, nil when
// none runs (or its token cannot be read).
func supervisorAsker(cfg config.Config) *restoreAsker {
	base := supervisor.StateURL(cfg)
	b, err := os.ReadFile(supervisor.LocalTokenPath(cfg))
	tok := strings.TrimSpace(string(b))
	if base == "" || err != nil || tok == "" {
		return nil
	}
	a := &restoreAsker{base: base, token: tok, http: &http.Client{Timeout: 2 * time.Minute}}
	req, _ := http.NewRequest(http.MethodGet, base+"/update/status", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	_ = resp.Body.Close()
	return a
}

func (a *restoreAsker) do(ctx context.Context, method, path string, body any) (map[string]json.RawMessage, int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var doc map[string]json.RawMessage
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("the server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if msg, ok := doc["error"]; ok {
		var s string
		_ = json.Unmarshal(msg, &s)
		return doc, resp.StatusCode, errors.New(s)
	}
	return doc, resp.StatusCode, nil
}

// runRestore is the command: the running server's restore, else this install's own.
func runRestore(ctx context.Context, out io.Writer, cfg config.Config, backup string, confirm bool) error {
	if n, err := supervisor.NodeOf(cfg); err == nil && n.Joined() {
		return errors.New("this machine is a node of a home: the library database is the home's, restored on the home")
	}
	name := filepath.Base(strings.TrimSpace(backup))
	if a := supervisorAsker(cfg); a != nil {
		return restoreThroughServer(ctx, out, a, name, confirm)
	}
	r, err := offlineRestorer(cfg)
	if err != nil {
		return err
	}
	if backup == "" {
		printBackups(out, r.list())
		return nil
	}
	pl, err := r.plan(ctx, name)
	if err != nil {
		return err
	}
	if !confirm {
		printPlan(out, pl, false)
		return errors.New("not restored: run it again with --confirm")
	}
	if _, err := r.restore(ctx, name, "the command line", true); err != nil {
		return err
	}
	printPlan(out, pl, true)
	fmt.Fprintln(out, "No server runs now: its next start puts the backup back (start the service).")
	return nil
}

func restoreThroughServer(ctx context.Context, out io.Writer, a *restoreAsker, name string, confirm bool) error {
	if name == "" || name == "." {
		doc, _, err := a.do(ctx, http.MethodGet, "/update/backups", nil)
		if err != nil {
			return err
		}
		var l backupList
		if err := json.Unmarshal(doc["backups"], &l); err != nil {
			return err
		}
		printBackups(out, l)
		return nil
	}
	if !confirm {
		doc, _, err := a.do(ctx, http.MethodGet, "/update/restore?backup="+url.QueryEscape(name), nil)
		if err != nil {
			return err
		}
		var pl restorePlan
		_ = json.Unmarshal(doc["plan"], &pl)
		printPlan(out, pl, false)
		return errors.New("not restored: run it again with --confirm")
	}
	doc, _, err := a.do(ctx, http.MethodPost, "/update/restore", restoreRequest{Backup: name, By: "the command line", Confirm: true})
	if err != nil {
		return err
	}
	var pl restorePlan
	_ = json.Unmarshal(doc["plan"], &pl)
	printPlan(out, pl, true)
	return nil
}

// offlineRestorer is this install's restorer when no server runs: a plain server's
// layout (the launcher starts the release the restore points at).
func offlineRestorer(cfg config.Config) (*restorer, error) {
	self, err := selfupdate.Self()
	if err != nil {
		return nil, err
	}
	l, ok := install.Root(self)
	if !ok {
		return nil, errors.New("no Maktaba server runs here: start it, then restore from the Updates page or run this " +
			"command again")
	}
	exe, _ := os.Executable()
	var pg *pgbundle.Bundle
	if b, found, err := pgbundle.Resolve(cfg, exe); err == nil && found {
		pg = &b
	}
	inst, _ := installKind(cfg, l.ReleaseDir(filepath.Base(filepath.Dir(self))), true, updateSource(cfg))
	return newRestorer(cfg, homeDatabase(cfg, pg), inst, nil), nil
}

func printBackups(out io.Writer, l backupList) {
	if l.Restoring != "" {
		fmt.Fprintf(out, "A restore of %s is under way: the next start of the server finishes it.\n\n", l.Restoring)
	}
	if len(l.Backups) == 0 {
		fmt.Fprintln(out, "This home has no backups.")
	}
	for _, b := range l.Backups {
		what := b.Kind
		if b.Kind == "update" {
			what = fmt.Sprintf("before the update from %s to %s", orUnknown(b.From), orUnknown(b.To))
		}
		fmt.Fprintf(out, "  %s  %s  %s  (%s)\n", b.At.Local().Format("2006-01-02 15:04"), sizeOf(b.Bytes), b.Name, what)
	}
	if len(l.Kept) > 0 {
		var kept []string
		for _, k := range l.Kept {
			kept = append(kept, k.Version)
		}
		fmt.Fprintf(out, "\nReleases kept here (a backup is put back on the one whose schema is its own): %s\n",
			strings.Join(kept, ", "))
	}
}

func printPlan(out io.Writer, pl restorePlan, done bool) {
	if done {
		fmt.Fprintf(out, "Restoring %s: the home restarts on %s and puts the backup back before anything else runs.\n",
			pl.Backup, pl.Release)
		return
	}
	fmt.Fprintf(out, "%s holds the database at schema %04d, taken %s.\n", pl.Backup, pl.Schema,
		pl.At.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(out, "Restoring it restarts the home on %s (it runs %s now) and loses every change made since.\n",
		pl.Release, orUnknown(pl.Running))
}

func sizeOf(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KiB", (n+1023)/1024)
	}
}
