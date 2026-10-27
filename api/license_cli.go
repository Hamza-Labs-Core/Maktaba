package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// runLicense is `maktaba-api license apply <file|->` and `maktaba-api license status`:
// the home's license from the command line (the owner's, without pasting it in
// Settings). The license is checked with the key this release was built with, as the
// Settings page checks it; the running API takes it up within a minute.
func runLicense(argv []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("license", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var dsn string
	fs.StringVar(&dsn, "dsn", "", "Database DSN (default: $DATABASE_URL)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `Usage:
  maktaba-api license apply <file>   apply a license (a file, or - for standard input)
  maktaba-api license status         the license this home has

Flags:`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	args := fs.Args()
	if len(args) == 0 || (args[0] == "apply" && len(args) != 2) || (args[0] == "status" && len(args) != 1) ||
		(args[0] != "apply" && args[0] != "status") {
		fs.Usage()
		return errors.New("expected 'apply <file>' or 'status'")
	}
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		return errors.New("DSN is empty: pass --dsn or set DATABASE_URL")
	}
	v, err := subscriptions.EmbeddedVerifier()
	if err != nil {
		return errors.New("this build of Maktaba cannot check licenses: install an official release to apply one")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping))
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	store, err := subscriptions.NewPersistentStore(ctx, subscriptions.NewSQLLicensePersistence(db), v)
	if err != nil && args[0] == "apply" && store == nil {
		return err
	}
	if args[0] == "status" {
		return printLicense(stdout, store.State())
	}

	var raw []byte
	if args[1] == "-" {
		raw, err = io.ReadAll(io.LimitReader(stdin, 32<<10))
	} else {
		raw, err = os.ReadFile(args[1])
	}
	if err != nil {
		return err
	}
	return applyLicense(ctx, store, v, router.LicenseAudit(db), raw, stdout)
}

// applyLicense checks and records one license: what the Settings page does, from the
// command line.
func applyLicense(ctx context.Context, store *subscriptions.Store, v *subscriptions.Verifier,
	audit func(ctx context.Context, actor, event string, detail map[string]any), raw []byte, stdout io.Writer) error {
	var lic subscriptions.License
	if err := json.Unmarshal(raw, &lic); err != nil {
		return errors.New("this is not a Maktaba license (a JSON object with the license and its signature)")
	}
	ent, err := v.Verify(&lic, store.Now())
	if err != nil {
		return fmt.Errorf("the license is refused: %w", err)
	}
	if st := store.State(); st.License != nil && st.Status == subscriptions.StatusRevoked && st.License.LicenseID == ent.LicenseID {
		return errors.New("this license was revoked by Maktaba")
	}
	if err := store.SetLicense(ctx, &lic, ent); err != nil {
		return err
	}
	if audit != nil {
		audit(ctx, "", "license.applied", map[string]any{"license_id": ent.LicenseID, "tier": string(ent.Tier), "via": "cli"})
	}
	fmt.Fprintln(stdout, "Applied. The running server takes it up within a minute.")
	return printLicense(stdout, store.State())
}

func printLicense(w io.Writer, st subscriptions.State) error {
	if st.License == nil {
		fmt.Fprintln(w, "No license: the Free plan.")
		return nil
	}
	l := st.License
	seats := "unlimited"
	if n := l.SeatLimit(); n >= 0 {
		seats = fmt.Sprint(n)
	}
	fmt.Fprintf(w, "License %s: %s plan, %s users, %s\n", l.LicenseID, l.Tier, seats, st.Status)
	if !l.ExpiresAt.IsZero() {
		fmt.Fprintf(w, "Valid until %s\n", l.ExpiresAt.Format("2006-01-02"))
	}
	return nil
}

// reloadLicense takes up, every `every`, a license another process recorded.
func reloadLicense(ctx context.Context, store *subscriptions.Store, v *subscriptions.Verifier, every time.Duration, logger *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		changed, err := store.ReloadActive(ctx, v)
		switch {
		case err != nil:
			logger.Warn("license: the recorded license cannot be taken up", "event", "license_reload_failed", "err", err)
		case changed:
			logger.Info("license: took up the recorded license", "event", "license_reloaded")
		}
	}
}
