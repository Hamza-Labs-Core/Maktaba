package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reviewhome"
)

// The environment the review home's commands read (docs/stores/review-home.md): the
// passwords come from the release secrets, never from a flag (a flag shows in ps) or a file
// in the repository.
const (
	envViewer         = "REVIEW_HOME_VIEWER"
	envViewerPassword = "REVIEW_HOME_VIEWER_PASSWORD"
	envChild          = "REVIEW_HOME_CHILD"
	envChildPassword  = "REVIEW_HOME_CHILD_PASSWORD"
)

// runReviewHome implements `maktaba-api review-home seed|rotate` (Epic 58 story 58.04).
func runReviewHome(argv []string, out io.Writer) error {
	if len(argv) == 0 || (argv[0] != "seed" && argv[0] != "rotate") {
		return errors.New(`usage: maktaba-api review-home seed [--manifest FILE] | rotate
  seed    the libraries of the content set, the viewer and the restricted child, their access,
          the child's limit, the live channel and each scanned item's titles and licence note
  rotate  new passwords for both accounts; every device they are signed in on is signed out
Passwords: $` + envViewerPassword + ` and $` + envChildPassword + ` (names: $` + envViewer + `, $` + envChild + `)`)
	}
	sub := argv[0]
	fs := flag.NewFlagSet("review-home "+sub, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var dsn, manifest string
	fs.StringVar(&dsn, "dsn", "", "Database DSN (default: $DATABASE_URL)")
	if sub == "seed" {
		fs.StringVar(&manifest, "manifest", "/srv/review-media/manifest.json", "The content set's manifest")
	}
	if err := fs.Parse(argv[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	a := accountsFromEnv(os.Getenv)
	if err := a.Check(); err != nil {
		return err
	}
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		return errors.New("DSN is empty: pass --dsn or set DATABASE_URL")
	}
	var m *reviewhome.Manifest
	if sub == "seed" {
		var err error
		if m, err = reviewhome.LoadManifest(manifest); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := sql.Open("postgres", pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping))
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	if sub == "rotate" {
		if err := reviewhome.Rotate(ctx, db, a); err != nil {
			return err
		}
		fmt.Fprintf(out, "review-home: new passwords for %s and %s; their devices are signed out\n", a.Viewer, a.Child)
		return nil
	}
	rep, err := reviewhome.Seed(ctx, db, m, a)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "review-home: viewer %s (%s), child %s (%s), %d libraries, channel %s, %d videos titled\n",
		a.Viewer, rep.ViewerID, a.Child, rep.ChildID, len(rep.Libraries), rep.ChannelID, rep.Titled)
	if len(rep.Missing) > 0 {
		// not a failure: the libraries' first scan has not reached them yet
		fmt.Fprintf(out, "review-home: not scanned yet (run seed again after the scan): %s\n", strings.Join(rep.Missing, ", "))
	}
	return nil
}

// accountsFromEnv reads the accounts' names (defaults: reviewer, reviewer-child) and passwords.
func accountsFromEnv(get func(string) string) reviewhome.Accounts {
	or := func(k, def string) string {
		if v := strings.TrimSpace(get(k)); v != "" {
			return v
		}
		return def
	}
	return reviewhome.Accounts{
		Viewer: or(envViewer, reviewhome.DefaultViewer), ViewerPassword: get(envViewerPassword),
		Child: or(envChild, reviewhome.DefaultChild), ChildPassword: get(envChildPassword),
	}
}
