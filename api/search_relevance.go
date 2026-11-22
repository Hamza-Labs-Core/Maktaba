package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/search"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
)

// runSearchRelevance is `maktaba-api search-relevance` (Epic 49 story 49.9): the relevance
// check on $DATABASE_URL's library, read only. It draws the searches from the library's own
// transcripts with a fixed seed (the same ones each run), runs them as keyword search does,
// and says how many passed and which did not. Exit status 1 when one did not.
func runSearchRelevance(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("search-relevance", flag.ContinueOnError)
	fs.SetOutput(out)
	o := search.DefaultRelevance
	fs.Int64Var(&o.Seed, "seed", o.Seed, "the generator's seed (the same searches for the same seed and library)")
	fs.IntVar(&o.KnownItem, "known-item", o.KnownItem, "distinctive sentences to draw (each in up to three spellings)")
	fs.IntVar(&o.Chapters, "chapters", o.Chapters, "chapter titles to draw")
	fs.IntVar(&o.Topics, "topics", o.Topics, "topic labels to draw")
	fs.IntVar(&o.Common, "common", o.Common, "most frequent content words, besides الله and الصبر")
	fs.IntVar(&o.Runs, "runs", o.Runs, "timed runs of each common word (p95)")
	asJSON := fs.Bool("json", false, "print the whole report as JSON")
	fs.Usage = func() {
		fmt.Fprintln(out, "Usage: maktaba-api search-relevance [flags]\n\nThe keyword search relevance check (story 49.9) on $DATABASE_URL's library, read only:\nsearches drawn from its own transcripts, each with what it must find.\n\nFlags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
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
	// read only: every statement of the check is a read
	if _, err := db.ExecContext(ctx, `SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY`); err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	t0 := time.Now()
	cases, err := search.GenerateRelevance(ctx, db, o)
	if err != nil {
		return err
	}
	rep, err := (&search.Handler{DB: db}).RunRelevance(ctx, cases, o.Runs)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(out, "%d searches in %s (seed %d)\n", len(cases), time.Since(t0).Round(time.Second), o.Seed)
		for _, k := range []string{search.RelKnownItem, search.RelChapter, search.RelTopic, search.RelCommon} {
			fmt.Fprintf(out, "  %-10s %d of %d passed\n", k, rep.Passed[k], rep.Total[k])
		}
		for _, r := range rep.Results {
			if r.Kind == search.RelCommon {
				fmt.Fprintf(out, "  common %q: p95 %s; %s\n", r.Query, r.P95.Round(time.Millisecond/10), r.Note)
			}
		}
		if rep.PhraseAbandoned > 0 {
			fmt.Fprintf(out, "  the phrase boost ran out of its budget %d times: those searches kept their BM25 order\n", rep.PhraseAbandoned)
		}
		for _, f := range rep.Failed() {
			fmt.Fprintf(out, "FAILED %s %s %q: rank %d, want the top %d %s\n", f.Kind, f.Variant, f.Query, f.Rank, f.Top, f.Note)
		}
	}
	if n := len(rep.Failed()); n > 0 {
		return fmt.Errorf("%d of %d searches did not pass", n, len(cases))
	}
	return nil
}
