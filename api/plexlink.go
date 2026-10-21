package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// runPlexLink implements `maktaba-api plex-link`: the end of "Sign in with
// Plex" for the desktop app's first-run setup (Epic 38 story 38.6). Setup
// ran plex.tv's PIN flow before this install had a database and saved the
// account token in a file of its own; once the database exists, this stores
// the Plex server the account reaches as the plex extension's (its address
// in the row, its token sealed in home_secrets), the same as the Settings
// page's sign-in, and prints what it chose as JSON. The account token is
// read from the file, never argv, and never stored.
func runPlexLink(argv []string, out io.Writer) error {
	fs := flag.NewFlagSet("plex-link", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var dsn, tokenFile, clientID, server string
	fs.StringVar(&dsn, "dsn", os.Getenv("DATABASE_URL"), "Database DSN (default: $DATABASE_URL)")
	fs.StringVar(&tokenFile, "token-file", "", "file holding the Plex account token (one line)")
	fs.StringVar(&clientID, "client-id", "", "the plex.tv client id the PIN was made with (kept as this install's)")
	fs.StringVar(&server, "server", "", "the Plex server to use when the account reaches several (default: the first on this network)")
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if tokenFile == "" || clientID == "" {
		return errors.New("--token-file and --client-id are required")
	}
	if dsn == "" {
		return errors.New("DSN is empty: pass --dsn or set DATABASE_URL")
	}
	raw, err := os.ReadFile(tokenFile) //nolint:gosec // the caller's own file
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return errors.New("the token file is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dsn = pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	key, err := secrets.LoadOrCreateKey(secrets.KeyPath(os.Getenv))
	if err != nil {
		return fmt.Errorf("secrets key: %w", err)
	}
	h := &queues.Handler{DB: db, Secrets: &secrets.DBStore{DB: db, Key: key}}
	linked, err := h.LinkPlex(ctx, clientID, token, server)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(linked)
}
