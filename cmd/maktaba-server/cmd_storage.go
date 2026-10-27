package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/selfupdate"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/storage"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/install"
)

// `maktaba-server storage` (Epic 36 story 36.15): where the install keeps its
// large state, and its move to another disk.
func newStorageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Where Maktaba keeps its data (show), and its move to another disk (move <dir>)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Each folder of Maktaba's data, its size and its disk's free space",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			printStorage(cmd.OutOrStdout(), storageDoc(cfg))
			return nil
		},
	})
	var dryRun bool
	move := &cobra.Command{
		Use:   "move <dir>",
		Short: "Copy Maktaba's data to <dir> (another disk), point server.toml at it, keep the old copy",
		Long: `move copies Maktaba's data (the library database, its backups, the models,
the caches, the data folder) to <dir>, a folder on another disk of this
computer, then sets [server] data_dir in server.toml to it.

Maktaba must be stopped first (quit the app or pause it from its menu, or stop
the service); Settings → Storage in the web does the same while it runs, and
stops and starts it itself.

It refuses a folder that is not empty, a disk without room for the copy (plus
a margin), and a disk the database cannot live on (a network share, FAT or
exFAT). It verifies the copy (every file and its size; the database starts on
it) before it changes anything, and keeps the old folders as
<folder>.moved-<stamp>: delete them yourself once Maktaba runs well from the
new place. Cut short, it is finished by running it again with the same <dir>;
until then Maktaba runs from where it was.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runStorageMove(ctx, cmd.OutOrStdout(), cfg, args[0], dryRun)
		},
	}
	move.Flags().BoolVar(&dryRun, "dry-run", false, "say what the move would do, and change nothing")
	cmd.AddCommand(move)
	return cmd
}

// storageInstall is cfg's install as the server.toml it came from has it.
func storageInstall(cfg config.Config) storage.Install {
	return storage.InstallOf(cfg, config.Path())
}

// storageDoc is the install's storage report, with whether it can move.
func storageDoc(cfg config.Config) storage.Doc {
	d := storage.Report(storageInstall(cfg), config.DefaultDataDir())
	if reason := cannotMove(cfg); reason != "" {
		d.CanMove, d.Reason = false, reason
	}
	return d
}

// cannotMove says why this install's data cannot move ("": it can).
func cannotMove(cfg config.Config) string {
	if path, ok := strings.CutPrefix(cfg.Database.URL, "sqlite://"); ok && !cfg.DatabaseURLSet {
		if _, err := os.Stat(path); err == nil {
			return "this install keeps its library in a SQLite file (" + path + "), which does not move with its data"
		}
	}
	if config.Container() != "" {
		return "this server runs in a container: its data is where the container's volume is"
	}
	return ""
}

// storageBusy says why the install's data cannot be copied now: a server runs (its
// admin answers, or its launcher holds the install's lock), or its database does.
// The locks it takes are held until release.
func storageBusy(cfg config.Config) (check func(storage.Layout) error, release func()) {
	var held []*os.File
	release = func() {
		for _, f := range held {
			layout.Unlock(f)
		}
	}
	check = func(from storage.Layout) error {
		if supervisorAsker(cfg) != nil {
			return errors.New("a Maktaba server runs here: stop it first (quit the app or pause it from its menu, or stop the " +
				"service), or move from Settings → Storage in the web, which stops and starts it itself")
		}
		// the launchers hold their install's lock for their life: the side-by-side
		// layout's, and the desktop app's beside server.toml
		var roots []layout.Layout
		if self, err := selfupdate.Self(); err == nil {
			if l, ok := install.Root(self); ok {
				roots = append(roots, l)
			}
		}
		if l := layout.New(config.LocalDirs().Config); dirExists(l.UpdatesDir()) {
			roots = append(roots, l)
		}
		for _, l := range roots {
			f, err := l.Lock()
			if errors.Is(err, layout.ErrLocked) {
				return fmt.Errorf("a Maktaba launcher runs here (it holds %s): stop Maktaba first", l.LockPath())
			}
			if err != nil {
				return err
			}
			held = append(held, f)
		}
		if pgbundleAt(from).Running() {
			return fmt.Errorf("the library database runs (%s holds a live postmaster.pid): stop Maktaba first",
				filepath.Join(from.Root, "postgres"))
		}
		return nil
	}
	return check, release
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// verifyPostgresFor starts the install's bundled Postgres on its copy in root
// (nil: the install runs none).
func verifyPostgresFor(cfg config.Config, log io.Writer) func(ctx context.Context, root string) error {
	exe, _ := os.Executable()
	b, ok, err := pgbundle.Resolve(cfg, exe)
	if err != nil || !ok {
		return nil
	}
	return func(ctx context.Context, root string) error {
		c := b
		c.Data, c.Backups, c.LAN = filepath.Join(root, "postgres"), filepath.Join(root, "backups"), false
		return c.Verify(ctx, log)
	}
}

func runStorageMove(ctx context.Context, out io.Writer, cfg config.Config, target string, dryRun bool) error {
	if reason := cannotMove(cfg); reason != "" {
		return errors.New("cannot move: " + reason)
	}
	in := storageInstall(cfg)
	if abs, err := filepath.Abs(strings.TrimSpace(target)); err == nil && strings.TrimSpace(target) != "" {
		target = abs
	}
	plan := storage.PlanMove(in, target)
	printMovePlan(out, plan)
	if !plan.OK {
		return &storage.PlanError{Plan: plan}
	}
	if dryRun {
		fmt.Fprintln(out, "Nothing was changed (--dry-run).")
		return nil
	}
	busy, release := storageBusy(cfg)
	defer release()
	last := time.Time{}
	res, err := storage.Move(ctx, in, plan.Target, storage.Options{
		Log:            out,
		Busy:           busy,
		VerifyPostgres: verifyPostgresFor(cfg, out),
		By:             "move",
		Progress: func(done, total uint64) {
			if time.Since(last) > 2*time.Second || done == total {
				last = time.Now()
				fmt.Fprintf(out, "  %s of %s copied (%d%%)\n", sizeOf(int64(done)), sizeOf(int64(total)), percentOf(done, total))
			}
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("the move was stopped: Maktaba still runs from %s; run this again to finish it", in.From.Data)
		}
		return err
	}
	fmt.Fprintf(out, "Maktaba's data is now in %s (server.toml says data_dir = %q).\n", res.Target, res.Target)
	printOldCopies(out, res.Old)
	fmt.Fprintln(out, "Start Maktaba again.")
	return nil
}

func percentOf(done, total uint64) uint64 {
	if total == 0 {
		return 100
	}
	return done * 100 / total
}

func printOldCopies(out io.Writer, old []string) {
	if len(old) == 0 {
		return
	}
	fmt.Fprintln(out, "The old copy is kept, in case anything went wrong:")
	for _, o := range old {
		fmt.Fprintf(out, "  %s\n", o)
	}
	fmt.Fprintln(out, "Once Maktaba runs well from the new place, remove it yourself, for example:")
	for _, o := range old {
		fmt.Fprintf(out, "  rm -rf %q\n", o)
	}
}

func printMovePlan(out io.Writer, p storage.Plan) {
	if p.Resume {
		fmt.Fprintf(out, "Finishing the move to %s that was cut short.\n", p.Target)
	} else if p.Target != "" {
		fmt.Fprintf(out, "Moving Maktaba's data from %s to %s:\n", p.From.Data, p.Target)
	}
	for _, e := range p.Entries {
		fmt.Fprintf(out, "  %-18s %10s  %s -> %s\n", e.Name, sizeOf(int64(e.Bytes)), e.From, e.To)
	}
	if p.Disk.Mount != "" {
		fmt.Fprintf(out, "Needs %s on %s, which has %s free.\n", sizeOf(int64(p.NeedBytes)), p.Disk.Mount,
			sizeOf(int64(p.FreeBytes)))
	}
	for _, n := range p.Warnings {
		fmt.Fprintf(out, "Note: %s\n", n.Message)
	}
	for _, n := range p.Problems {
		fmt.Fprintf(out, "Cannot move: %s\n", n.Message)
	}
}

func printStorage(out io.Writer, d storage.Doc) {
	fmt.Fprintf(out, "Data folder ([server] data_dir): %s\n", d.DataDir)
	if d.StateDir != d.DataDir {
		fmt.Fprintf(out, "State folders beside it, in: %s\n", d.StateDir)
	}
	fmt.Fprintln(out)
	for _, f := range d.Folders {
		if !f.Exists {
			continue
		}
		fmt.Fprintf(out, "  %-18s %10s  %6d files  %s\n", f.Name, sizeOf(int64(f.Bytes)), f.Files, f.Path)
	}
	fmt.Fprintf(out, "  %-18s %10s\n\n", "total", sizeOf(int64(d.TotalBytes)))
	for _, k := range d.Disks {
		fmt.Fprintf(out, "Disk %s (%s, %s): %s free of %s\n", k.Mount, k.Name, k.FSType, sizeOf(int64(k.FreeBytes)),
			sizeOf(int64(k.TotalBytes)))
	}
	switch d.Move.State {
	case "requested":
		fmt.Fprintf(out, "\nA move to %s was asked for: the server's next start does it.\n", d.Move.Target)
	case "moving":
		fmt.Fprintf(out, "\nA move to %s is not finished: run `maktaba-server storage move %q` (Maktaba stopped) to finish it.\n",
			d.Move.Target, d.Move.Target)
	case "failed":
		fmt.Fprintf(out, "\nThe last move, to %s, did not happen: %s\n", d.Move.Target, d.Move.Error)
	case "done":
		if len(d.Move.Old) > 0 {
			fmt.Fprintln(out)
			printOldCopies(out, d.Move.Old)
		}
	}
	if !d.CanMove {
		fmt.Fprintf(out, "\nThis install's data cannot move: %s\n", d.Reason)
	}
}
