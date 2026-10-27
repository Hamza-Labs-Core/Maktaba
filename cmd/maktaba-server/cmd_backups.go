package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// `maktaba-server backups` (Epic 36 story 36.16): the backups' settings and dumps.
// With the server running it goes through its admin (as the web does); with none,
// it changes server.toml itself (the next start takes it).
func newBackupsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backups",
		Short: "The library database's backups: where they go, when, how many stay (show, set, delete <name>)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "The backups' settings, the dumps there are and the free space of their disk",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			doc, err := backupsShow(cmd.Context())
			if err != nil {
				return err
			}
			printBackupsDoc(cmd.OutOrStdout(), doc)
			return nil
		},
	})
	var (
		dir, nightly, preUpdate, moveDumps                        string
		every, hour, keep, updateKeep, reserve, reserveMin, share int
	)
	set := &cobra.Command{
		Use:   "set",
		Short: "Change the backups' settings (only the flags given change)",
		Long: `set changes the backups' settings in server.toml ([backups]); the server takes them at
its next start (Settings → Storage restarts it).

  --dir <folder>            where the dumps go ("" : beside the database)
  --nightly on|off          the scheduled dump
  --every-days N            a dump every N days (1: daily, 7: weekly)
  --hour H                  from H o'clock local time
  --keep N                  scheduled dumps kept
  --update-keep N           dumps taken before an update kept (0: as the update settings say)
  --pre-update on|off       the dump before an update's migrations (off: no way back when they fail)
  --reserve-percent P       the share of the disk a dump leaves free
  --reserve-min-gb G        the least it leaves free
  --max-share-percent P     the most of the disk the dumps take together
  --move-dumps yes|no       with --dir: move the dumps there are to the new folder, or leave them`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var ch backupsChange
			f := cmd.Flags()
			onOff := func(name, v string) (*bool, error) {
				switch strings.ToLower(strings.TrimSpace(v)) {
				case "on", "yes", "true":
					b := true
					return &b, nil
				case "off", "no", "false":
					b := false
					return &b, nil
				}
				return nil, fmt.Errorf("--%s is on or off, not %q", name, v)
			}
			var err error
			if f.Changed("dir") {
				ch.Dir = &dir
			}
			if f.Changed("nightly") {
				if ch.Nightly, err = onOff("nightly", nightly); err != nil {
					return err
				}
			}
			if f.Changed("pre-update") {
				if ch.PreUpdate, err = onOff("pre-update", preUpdate); err != nil {
					return err
				}
			}
			if f.Changed("move-dumps") {
				if ch.MoveDumps, err = onOff("move-dumps", moveDumps); err != nil {
					return err
				}
			}
			ints := map[string]**int{"every-days": &ch.EveryDays, "hour": &ch.Hour, "keep": &ch.Keep,
				"update-keep": &ch.UpdateKeep, "reserve-percent": &ch.ReservePercent, "reserve-min-gb": &ch.ReserveMinGB,
				"max-share-percent": &ch.MaxSharePercent}
			vals := map[string]*int{"every-days": &every, "hour": &hour, "keep": &keep, "update-keep": &updateKeep,
				"reserve-percent": &reserve, "reserve-min-gb": &reserveMin, "max-share-percent": &share}
			for name, dst := range ints {
				if f.Changed(name) {
					*dst = vals[name]
				}
			}
			doc, err := backupsSet(cmd.Context(), ch)
			if err != nil {
				return err
			}
			printBackupsDoc(cmd.OutOrStdout(), doc)
			return nil
		},
	}
	sf := set.Flags()
	sf.StringVar(&dir, "dir", "", "where the dumps go (\"\": beside the database)")
	sf.StringVar(&nightly, "nightly", "", "on|off")
	sf.StringVar(&preUpdate, "pre-update", "", "on|off")
	sf.StringVar(&moveDumps, "move-dumps", "", "yes|no (with --dir)")
	sf.IntVar(&every, "every-days", 1, "a dump every N days")
	sf.IntVar(&hour, "hour", config.DefaultBackupHour, "the local hour")
	sf.IntVar(&keep, "keep", config.DefaultBackupKeep, "scheduled dumps kept")
	sf.IntVar(&updateKeep, "update-keep", 0, "pre-update dumps kept")
	sf.IntVar(&reserve, "reserve-percent", config.DefaultReservePercent, "share of the disk left free")
	sf.IntVar(&reserveMin, "reserve-min-gb", config.DefaultReserveMinGB, "the least left free, GB")
	sf.IntVar(&share, "max-share-percent", config.DefaultMaxSharePercent, "the most of the disk the dumps take")
	cmd.AddCommand(set)
	cmd.AddCommand(&cobra.Command{
		Use:   "delete <name>",
		Short: "Delete one dump of the backups folder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a := backupsAsker(); a != nil {
				_, _, err := a.do(cmd.Context(), http.MethodDelete, "/config/backups/dumps/"+url.PathEscape(args[0]), nil)
				return err
			}
			return deleteBackup(config.Path(), args[0])
		},
	})
	return cmd
}

// backupsAsker is the running supervisor's admin (nil: none answers).
var backupsAsker = func() *restoreAsker {
	cfg, _, err := config.Load()
	if err != nil {
		return nil
	}
	return supervisorAsker(cfg)
}

func backupsShow(ctx context.Context) (backupsDoc, error) {
	if a := backupsAsker(); a != nil {
		return askBackups(ctx, a, http.MethodGet, nil)
	}
	return backupsDocAt(config.Path(), nil, false)
}

func backupsSet(ctx context.Context, ch backupsChange) (backupsDoc, error) {
	if a := backupsAsker(); a != nil {
		return askBackups(ctx, a, http.MethodPut, ch)
	}
	if _, err := applyBackups(config.Path(), ch); err != nil {
		return backupsDoc{}, err
	}
	return backupsDocAt(config.Path(), nil, false)
}

func askBackups(ctx context.Context, a *restoreAsker, method string, body any) (backupsDoc, error) {
	raw, code, err := a.do(ctx, method, "/config/backups", body)
	var doc backupsDoc
	if code == http.StatusUnprocessableEntity {
		var ref backupsRefusal
		b, _ := json.Marshal(raw)
		if json.Unmarshal(b, &ref) == nil && len(ref.Errors) > 0 {
			return doc, &ref
		}
	}
	if err != nil {
		return doc, err
	}
	b, _ := json.Marshal(raw)
	return doc, json.Unmarshal(b, &doc)
}

func printBackupsDoc(out io.Writer, d backupsDoc) {
	s := d.Saved
	onOff := map[bool]string{true: "on", false: "off"}
	fmt.Fprintf(out, "Backups folder:      %s\n", d.Dir)
	if d.Disk != nil {
		fmt.Fprintf(out, "Its disk:            %s, %s free of %s\n", d.Disk.Mount, humanBytes(d.Disk.FreeBytes),
			humanBytes(d.Disk.TotalBytes))
	}
	every := fmt.Sprintf("every %d days", s.EveryDays)
	switch s.EveryDays {
	case 1:
		every = "every day"
	case 7:
		every = "every week"
	}
	fmt.Fprintf(out, "Scheduled dump:      %s, %s from %02d:00, the newest %d kept\n", onOff[s.Nightly], every, s.Hour, s.Keep)
	fmt.Fprintf(out, "Before an update:    %s, the newest %d kept\n", onOff[s.PreUpdate], d.UpdateKeep)
	fmt.Fprintf(out, "Room on the disk:    %d%% left free (at least %d GB), the dumps within %d%% of it\n",
		s.ReservePercent, s.ReserveMinGB, s.MaxSharePercent)
	if !s.PreUpdate {
		fmt.Fprintln(out, "Warning: with no dump before an update, an update whose migrations fail cannot put the database back.")
	}
	if d.RestartNeeded {
		fmt.Fprintln(out, "The server runs with other settings: they apply at its next start.")
	}
	fmt.Fprintf(out, "\n%d dumps, %s:\n", len(d.Dumps), sizeOf(d.DumpsBytes))
	for _, x := range d.Dumps {
		fmt.Fprintf(out, "  %s  %-8s %10s  %s\n", x.At.Local().Format("2006-01-02 15:04"), x.Kind, sizeOf(x.Bytes), x.Name)
	}
}
