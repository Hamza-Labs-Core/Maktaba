package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/searchindex"
)

// runSearchIndexes is `maktaba-api search-indexes` (Epic 49 stories 49.5, 49.6): builds the
// keyword search indexes the database lacks now, in the foreground and with no time limit,
// as the home API's api:search_index loop does after its start. For an operator who wants
// them before searching, and for the payload checks (tools/release/pg18-check.sh).
func runSearchIndexes(args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(out, "Usage: maktaba-api search-indexes\n\nBuilds keyword search's BM25 indexes that $DATABASE_URL's database lacks\n(PostgreSQL 18 with pg_textsearch; nothing elsewhere), then says whether both are ready.")
		return nil
	}
	if nodemode.FromEnv(os.Getenv).Enabled {
		return errors.New("a machine builds no search index: its home does")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is empty")
	}
	db, err := pgdsn.Open(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ran, locked, err := searchindex.BuildOnce(ctx, db)
	for _, s := range ran {
		fmt.Fprintf(out, "built: %s\n", s)
	}
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("another API is building the search indexes now: try again when it is done")
	}
	ready, err := searchindex.Ready(ctx, db)
	if err != nil {
		return err
	}
	if ready {
		fmt.Fprintln(out, "keyword search indexes: ready")
	} else {
		fmt.Fprintln(out, "keyword search indexes: none (no pg_textsearch in this database)")
	}
	return nil
}
